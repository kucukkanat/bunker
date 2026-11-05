package data

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// Live updates.
//
// Every SSE connection is served by its own request goroutine; it never
// starts more. Bus topics are shared: the hub subscribes to a bus topic
// once, when the first local connection needs it, and runs one forwarder
// goroutine per active topic that fans messages out to the connections'
// buffered queues (dropping for connections that fall behind).

const (
	heartbeat      = 25 * time.Second
	presenceTTL    = 40 * time.Second
	maxChannels    = 32
	connQueue      = 256
	maxRoomMsg     = 64 << 10
	maxPresence    = 4 << 10
	maxRoomMembers = 500
)

func maxConnsPerUser() int { return core.EnvInt("EVENTS_MAX_PER_USER", 20) }
func maxConnsTotal() int   { return core.EnvInt("EVENTS_MAX", 20000) }

var hub *Hub

// Hub fans bus topics out to SSE connections.
type Hub struct {
	a       *core.App
	mu      sync.Mutex
	topics  map[string]*topicSubs
	perUser map[int64]int
	total   int
	done    chan struct{}
	once    sync.Once

	presMu sync.Mutex
	rooms  map[string]map[int64]*Member // "site/room" → members

	limMu sync.Mutex
	lims  map[string]*bucket
}

type topicSubs struct {
	mu     sync.RWMutex
	conns  map[*sseConn]*sub
	cancel func()
	// For db:<coll> topics: the collection, so forward can parse each
	// change and look up the collection rule once for all connections.
	site int64
	coll string
}

type sub struct {
	ch   string // client channel name, e.g. "db:orders"
	kind byte   // 'd' db, 'r' room, 'p' presence, 'x' deploy, 'f' draft
	coll string
}

// evt is one queued delivery. It is two pointers (16 bytes), so each
// connection's queue of connQueue events costs 4 KB.
type evt struct {
	sub *sub
	m   *busMsg
}

// busMsg is a bus message prepared once for every connection on a topic.
type busMsg struct {
	msg []byte
	// db changes: author, client payload and the collection rule.
	db     bool
	ok     bool
	author int64
	data   []byte
	rule   string
}

type sseConn struct {
	out     chan evt
	dropped atomic.Int64
}

// Member is one person present in a room.
type Member struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	State    json.RawMessage `json:"state"`
	LastSeen int64           `json:"lastSeen"`
}

func newHub(a *core.App) *Hub {
	h := &Hub{a: a, topics: map[string]*topicSubs{}, perUser: map[int64]int{}, done: make(chan struct{}),
		rooms: map[string]map[int64]*Member{}, lims: map[string]*bucket{}}
	a.On("shutdown", func(ctx context.Context, _ any) { h.Close() })
	go h.janitor()
	return h
}

// Close ends every live connection (server shutdown).
func (h *Hub) Close() { h.once.Do(func() { close(h.done) }) }

func (h *Hub) acquire(uid int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= maxConnsTotal() || h.perUser[uid] >= maxConnsPerUser() {
		return false
	}
	h.total++
	h.perUser[uid]++
	return true
}

func (h *Hub) release(uid int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total--
	if h.perUser[uid]--; h.perUser[uid] <= 0 {
		delete(h.perUser, uid)
	}
}

func (h *Hub) subscribe(c *sseConn, topic string, site int64, s sub) {
	h.mu.Lock()
	ts := h.topics[topic]
	if ts == nil {
		ch, cancel := h.a.Bus.Subscribe(topic)
		ts = &topicSubs{conns: map[*sseConn]*sub{}, cancel: cancel}
		if s.kind == 'd' {
			ts.site, ts.coll = site, s.coll
		}
		h.topics[topic] = ts
		go forward(ts, ch)
	}
	ts.mu.Lock()
	ts.conns[c] = &s
	ts.mu.Unlock()
	h.mu.Unlock()
}

func (h *Hub) unsubscribe(c *sseConn, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ts := h.topics[topic]
	if ts == nil {
		return
	}
	ts.mu.Lock()
	delete(ts.conns, c)
	empty := len(ts.conns) == 0
	ts.mu.Unlock()
	if empty {
		delete(h.topics, topic)
		ts.cancel() // closes the bus channel; forward exits
	}
}

func forward(ts *topicSubs, ch <-chan []byte) {
	for msg := range ch {
		m := &busMsg{msg: msg}
		if ts.coll != "" {
			m.db = true
			m.author, m.data, m.ok = splitDBMsg(msg)
			if m.ok && m.author != 0 {
				m.rule = CollectionRule(context.Background(), ts.site, ts.coll)
			}
		}
		ts.mu.RLock()
		for c, s := range ts.conns {
			select {
			case c.out <- evt{sub: s, m: m}:
			default:
				c.dropped.Add(1)
			}
		}
		ts.mu.RUnlock()
	}
}

// Stats reports live connection counts (for tests and admin views).
func (h *Hub) Stats() (conns, topics int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total, len(h.topics)
}

// ---- channel parsing ----

type chanReq struct {
	topic string
	s     sub
}

func parseChannels(siteID int64, role core.Role, list string) ([]chanReq, error) {
	var out []chanReq
	seen := map[string]bool{}
	base := "site:" + i64(siteID) + ":"
	for _, ch := range strings.Split(list, ",") {
		ch = strings.TrimSpace(ch)
		if ch == "" || seen[ch] {
			continue
		}
		seen[ch] = true
		if len(seen) > maxChannels {
			return nil, core.BadRequest("too many channels on one connection (32 at most)")
		}
		kind, name, _ := strings.Cut(ch, ":")
		switch kind {
		case "deploy":
			out = append(out, chanReq{base + "deploy", sub{ch: ch, kind: 'x'}})
			if role >= core.RoleEditor {
				out = append(out, chanReq{base + "draft", sub{ch: ch, kind: 'f'}})
			}
		case "logs":
			// Server function logs: editors only, the code and its output are theirs.
			if role < core.RoleEditor {
				return nil, core.Forbidden("only editors can follow server function logs")
			}
			out = append(out, chanReq{base + "logs", sub{ch: ch, kind: 'x'}})
		case "db":
			if err := checkColl(name); err != nil {
				return nil, err
			}
			out = append(out, chanReq{base + "db:" + name, sub{ch: ch, kind: 'd', coll: name}})
		case "room":
			if !nameRe.MatchString(name) {
				return nil, core.BadRequest("room names use up to 64 letters, numbers, dots, dashes and underscores")
			}
			out = append(out, chanReq{base + "room:" + name, sub{ch: ch, kind: 'r'}})
		case "presence":
			if !nameRe.MatchString(name) {
				return nil, core.BadRequest("room names use up to 64 letters, numbers, dots, dashes and underscores")
			}
			out = append(out, chanReq{base + "presence:" + name, sub{ch: ch, kind: 'p', coll: name}})
		default:
			return nil, core.BadRequest("unknown channel " + ch + ". Use db:<collection>, room:<name>, presence:<room>, deploy or logs.")
		}
	}
	return out, nil
}

// splitDBMsg parses {"author":N,"data":...} without decoding the document.
func splitDBMsg(msg []byte) (int64, []byte, bool) {
	const pre = `{"author":`
	if !bytes.HasPrefix(msg, []byte(pre)) {
		return 0, nil, false
	}
	rest := msg[len(pre):]
	i := bytes.IndexByte(rest, ',')
	if i < 0 {
		return 0, nil, false
	}
	n, err := strconv.ParseInt(string(rest[:i]), 10, 64)
	if err != nil {
		return 0, nil, false
	}
	rest = rest[i+1:]
	const dp = `"data":`
	if !bytes.HasPrefix(rest, []byte(dp)) || len(rest) < len(dp)+1 {
		return 0, nil, false
	}
	return n, rest[len(dp) : len(rest)-1], true
}

// ---- SSE endpoint ----

func registerEvents(a *core.App) {
	a.Handle(core.Op{Name: "events.stream", Method: "GET", Path: "/api/v1/sites/{site}/events", Summary: "Live updates (server-sent events) for data changes, rooms, presence and new versions", PageSafe: true, Stream: true,
		Params:  []core.Param{siteParam, {Name: "ch", In: "query", Required: true, Desc: "Comma-separated channels: db:<collection>, room:<name>, presence:<room>, deploy, logs (editors)"}},
		Handler: func(w http.ResponseWriter, r *http.Request) { hub.serveSSE(w, r) }})

	a.Handle(core.Op{Name: "rooms.send", Method: "POST", Path: "/api/v1/sites/{site}/rooms/{room}", Summary: "Send a message to everyone in a room (not stored)", PageSafe: true,
		Params: []core.Param{siteParam, {Name: "room", In: "path", Required: true}, {Name: "data", In: "body", Required: true, Desc: "Any JSON value (64 KB at most)"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			room := r.PathValue("room")
			if !nameRe.MatchString(room) {
				return nil, core.BadRequest("room names use up to 64 letters, numbers, dots, dashes and underscores")
			}
			if !hub.allow("room:"+i64(s.ID)+":"+i64(u.ID), 30, 60) {
				return nil, core.Quota("too many messages; slow down a little")
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, maxRoomMsg+1024))
			if err != nil || len(raw) > maxRoomMsg+512 {
				return nil, core.TooLarge("room messages can be 64 KB at most")
			}
			var in struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, core.BadRequest("send JSON like {\"data\": ...}")
			}
			data := compactJSON(in.Data)
			msg, _ := json.Marshal(map[string]any{"type": "message", "from": map[string]any{"id": u.ID, "name": u.Name}, "data": data, "ts": nowMS()})
			a.Bus.Publish("site:"+i64(s.ID)+":room:"+room, msg)
			return map[string]any{"ok": true, "listeners": a.Bus.Count("site:" + i64(s.ID) + ":room:" + room)}, nil
		}})

	a.Handle(core.Op{Name: "rooms.presence", Method: "POST", Path: "/api/v1/sites/{site}/rooms/{room}/presence", Summary: "Say you're here (refresh every ~15 s) and get who else is", PageSafe: true,
		Params: []core.Param{siteParam, {Name: "room", In: "path", Required: true}, {Name: "state", In: "body", Desc: "Small JSON value describing you (4 KB at most)"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			room := r.PathValue("room")
			if !nameRe.MatchString(room) {
				return nil, core.BadRequest("room names use up to 64 letters, numbers, dots, dashes and underscores")
			}
			if !hub.allow("pres:"+i64(s.ID)+":"+i64(u.ID), 10, 20) {
				return nil, core.Quota("too many presence updates; slow down a little")
			}
			raw, _ := io.ReadAll(io.LimitReader(r.Body, maxPresence+1024))
			var in struct {
				State json.RawMessage `json:"state"`
			}
			if len(bytes.TrimSpace(raw)) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, core.BadRequest("send JSON like {\"state\": ...}")
				}
			}
			st := compactJSON(in.State)
			if len(st) > maxPresence {
				return nil, core.TooLarge("presence state can be 4 KB at most")
			}
			members, err := hub.setPresence(s.ID, room, u, st)
			if err != nil {
				return nil, err
			}
			return map[string]any{"members": members}, nil
		}})

	a.Handle(core.Op{Name: "rooms.leave", Method: "DELETE", Path: "/api/v1/sites/{site}/rooms/{room}/presence", Summary: "Leave a room's presence list", PageSafe: true,
		Params: []core.Param{siteParam, {Name: "room", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			room := r.PathValue("room")
			if !nameRe.MatchString(room) {
				return nil, core.BadRequest("invalid room name")
			}
			hub.leave(s.ID, room, u.ID)
			return nil, nil
		}})

	a.Handle(core.Op{Name: "rooms.members", Method: "GET", Path: "/api/v1/sites/{site}/rooms/{room}/presence", Summary: "Who is in a room right now", PageSafe: true,
		Params: []core.Param{siteParam, {Name: "room", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			s, _, _, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			room := r.PathValue("room")
			if !nameRe.MatchString(room) {
				return nil, core.BadRequest("invalid room name")
			}
			return map[string]any{"members": hub.members(s.ID, room)}, nil
		}})
}

func compactJSON(b json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(b)) == 0 {
		return json.RawMessage("null")
	}
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return json.RawMessage("null")
	}
	return buf.Bytes()
}

func (h *Hub) serveSSE(w http.ResponseWriter, r *http.Request) {
	s, role, u, err := siteFor(r, core.RoleViewer)
	if err != nil {
		core.Err(w, err)
		return
	}
	reqs, err := parseChannels(s.ID, role, r.URL.Query().Get("ch"))
	if err != nil {
		core.Err(w, err)
		return
	}
	select {
	case <-h.done:
		core.Err(w, core.Unavailable("the server is restarting; try again in a moment"))
		return
	default:
	}
	if !h.acquire(u.ID) {
		core.Err(w, core.Quota("too many live connections open; close some tabs and reload"))
		return
	}
	defer h.release(u.ID)

	rc := http.NewResponseController(w)
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(b []byte) error {
		rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := w.Write(b); err != nil {
			return err
		}
		return nil
	}
	if write([]byte("retry: 3000\n: connected\n\n")) != nil || rc.Flush() != nil {
		return
	}

	c := &sseConn{out: make(chan evt, connQueue)}
	for _, q := range reqs {
		h.subscribe(c, q.topic, s.ID, q.s)
	}
	defer func() {
		for _, q := range reqs {
			h.unsubscribe(c, q.topic)
		}
	}()
	// Initial presence snapshots.
	for _, q := range reqs {
		if q.s.kind == 'p' {
			msg, _ := json.Marshal(map[string]any{"type": "sync", "members": h.members(s.ID, q.s.coll)})
			if write(frame(q.s.ch, msg)) != nil {
				return
			}
		}
	}
	rc.Flush()

	ctx := r.Context()
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	var buf bytes.Buffer
	// The viewer's role is re-resolved for an event when site caches were
	// invalidated since the last check (shares, updates) or a second has
	// passed, instead of on every event.
	gens, _ := h.a.Sites.(interface{ Generation() uint64 })
	var roleGen uint64
	var roleAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.done:
			write([]byte("event: bye\ndata: {}\n\n"))
			rc.Flush()
			return
		case <-tick.C:
			// Re-check access so revoking a share closes live streams too.
			if _, ro, err := h.a.Sites.Resolve(ctx, u, s.Slug); err != nil || ro == core.RoleNone {
				return
			}
			if write([]byte(": hb\n\n")) != nil || rc.Flush() != nil {
				return
			}
		case e := <-c.out:
			// Use the viewer's current role (cached; shares invalidate it), so
			// a demotion or removal applies to the next event, not the next reconnect.
			now := time.Now()
			if gens == nil || gens.Generation() != roleGen || now.Sub(roleAt) >= time.Second {
				if gens != nil {
					roleGen = gens.Generation() // read before resolving
				}
				if _, ro, err := h.a.Sites.Resolve(ctx, u, s.Slug); err != nil || ro == core.RoleNone {
					return
				} else {
					role = ro
				}
				roleAt = now
			}
			buf.Reset()
			wrote := false
			h.render(&buf, e, s.ID, u, role)
			// Drain whatever else is queued, then flush once. Rendered
			// frames are handed to the response writer every 4 KB, so the
			// buffer stays small with thousands of connections.
		drain:
			for i := 0; i < 64; i++ {
				select {
				case e2 := <-c.out:
					h.render(&buf, e2, s.ID, u, role)
					if buf.Len() >= 4096 {
						if write(buf.Bytes()) != nil {
							return
						}
						buf.Reset()
						wrote = true
					}
				default:
					break drain
				}
			}
			if buf.Len() == 0 && !wrote {
				continue
			}
			if buf.Len() > 0 && write(buf.Bytes()) != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

func frame(ch string, payload []byte) []byte {
	var b bytes.Buffer
	writeFrame(&b, ch, payload)
	return b.Bytes()
}

func writeFrame(b *bytes.Buffer, ch string, payload []byte) {
	b.WriteString(`data: {"ch":"`)
	b.WriteString(ch) // validated: no quotes or backslashes
	b.WriteString(`","data":`)
	if bytes.IndexByte(payload, '\n') >= 0 {
		payload = bytes.ReplaceAll(payload, []byte("\n"), []byte(" "))
	}
	b.Write(payload)
	b.WriteString("}\n\n")
}

// render writes e to b if this viewer may see it.
func (h *Hub) render(b *bytes.Buffer, e evt, siteID int64, u *core.User, role core.Role) {
	payload := e.m.msg
	if e.sub.kind == 'd' {
		m := e.m
		if !m.db { // not prepared by forward (defensive)
			m = &busMsg{db: true}
			m.author, m.data, m.ok = splitDBMsg(e.m.msg)
			if m.ok && m.author != 0 {
				m.rule = CollectionRule(context.Background(), siteID, e.sub.coll)
			}
		}
		if !m.ok {
			return
		}
		if m.author != 0 { // 0 = collection-level notice (dropped/reset)
			if !AccessFor(u, role, m.rule).CanRead(u.ID, m.author) {
				return
			}
		}
		payload = m.data
	}
	writeFrame(b, e.sub.ch, payload)
}

// ---- presence ----

func roomKey(site int64, room string) string { return i64(site) + "/" + room }

func (h *Hub) setPresence(site int64, room string, u *core.User, state json.RawMessage) ([]Member, error) {
	k := roomKey(site, room)
	h.presMu.Lock()
	ms := h.rooms[k]
	if ms == nil {
		if len(h.rooms) > 20000 {
			h.presMu.Unlock()
			return nil, core.Quota("too many active rooms right now; try again later")
		}
		ms = map[int64]*Member{}
		h.rooms[k] = ms
	}
	m, existed := ms[u.ID]
	if !existed && len(ms) >= maxRoomMembers {
		h.presMu.Unlock()
		return nil, core.Quota("this room is full")
	}
	changed := !existed || !bytes.Equal(m.State, state)
	if !existed {
		m = &Member{ID: u.ID, Name: u.Name}
		ms[u.ID] = m
	}
	m.State = state
	m.LastSeen = nowMS()
	list := snapshot(ms)
	h.presMu.Unlock()
	if changed {
		typ := "update"
		if !existed {
			typ = "join"
		}
		h.publishPresence(site, room, typ, *m, list)
	}
	return list, nil
}

func (h *Hub) leave(site int64, room string, uid int64) {
	k := roomKey(site, room)
	h.presMu.Lock()
	ms := h.rooms[k]
	m, ok := ms[uid]
	if !ok {
		h.presMu.Unlock()
		return
	}
	delete(ms, uid)
	list := snapshot(ms)
	if len(ms) == 0 {
		delete(h.rooms, k)
	}
	h.presMu.Unlock()
	h.publishPresence(site, room, "leave", *m, list)
}

func (h *Hub) members(site int64, room string) []Member {
	h.presMu.Lock()
	defer h.presMu.Unlock()
	return snapshot(h.rooms[roomKey(site, room)])
}

func snapshot(ms map[int64]*Member) []Member {
	out := make([]Member, 0, len(ms))
	for _, m := range ms {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (h *Hub) publishPresence(site int64, room, typ string, m Member, list []Member) {
	msg, _ := json.Marshal(map[string]any{"type": typ, "user": m, "members": list})
	h.a.Bus.Publish("site:"+i64(site)+":presence:"+room, msg)
}

// janitor expires presence and prunes rate limiters.
func (h *Hub) janitor() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-t.C:
		}
		cut := nowMS() - presenceTTL.Milliseconds()
		type gone struct {
			site int64
			room string
			m    Member
			list []Member
		}
		var left []gone
		h.presMu.Lock()
		for k, ms := range h.rooms {
			for id, m := range ms {
				if m.LastSeen < cut {
					delete(ms, id)
					site, room, _ := strings.Cut(k, "/")
					sid, _ := strconv.ParseInt(site, 10, 64)
					left = append(left, gone{sid, room, *m, nil})
				}
			}
			if len(ms) == 0 {
				delete(h.rooms, k)
			}
		}
		for i := range left {
			left[i].list = snapshot(h.rooms[roomKey(left[i].site, left[i].room)])
		}
		h.presMu.Unlock()
		for _, g := range left {
			h.publishPresence(g.site, g.room, "leave", g.m, g.list)
		}
		now := time.Now()
		h.limMu.Lock()
		for k, b := range h.lims {
			if now.Sub(b.last) > time.Minute {
				delete(h.lims, k)
			}
		}
		h.limMu.Unlock()
	}
}

// ---- rate limiting (token bucket) ----

type bucket struct {
	tokens float64
	last   time.Time
}

// allow takes a token from key's bucket (rate per second, burst capacity).
func (h *Hub) allow(key string, rate, burst float64) bool {
	now := time.Now()
	h.limMu.Lock()
	defer h.limMu.Unlock()
	b := h.lims[key]
	if b == nil {
		if len(h.lims) > 100000 {
			h.lims = map[string]*bucket{}
		}
		b = &bucket{tokens: burst, last: now}
		h.lims[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
