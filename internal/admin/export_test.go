package admin

// StubRestart replaces the restart after an update; it returns a channel
// that receives once per requested restart.
func StubRestart() (restarts chan struct{}, undo func()) {
	ch := make(chan struct{}, 4)
	old, oldDelay := requestRestart, restartDelay
	requestRestart, restartDelay = func() { ch <- struct{}{} }, 0
	return ch, func() {
		requestRestart, restartDelay = old, oldDelay
		setJob(func(j *updateJob) { *j = updateJob{State: "idle"} })
	}
}
