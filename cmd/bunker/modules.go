package main

// Feature modules register themselves in init(). Add one blank import per module.
import (
	_ "github.com/kucukkanat/bunker/internal/admin"
	_ "github.com/kucukkanat/bunker/internal/ai"
	_ "github.com/kucukkanat/bunker/internal/apptypes"
	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/connections"
	_ "github.com/kucukkanat/bunker/internal/data"
	_ "github.com/kucukkanat/bunker/internal/entra"
	_ "github.com/kucukkanat/bunker/internal/files"
	_ "github.com/kucukkanat/bunker/internal/hooks"
	_ "github.com/kucukkanat/bunker/internal/m365"
	_ "github.com/kucukkanat/bunker/internal/mcp"
	_ "github.com/kucukkanat/bunker/internal/notify"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/templates"
	_ "github.com/kucukkanat/bunker/internal/tlsauto"
	_ "github.com/kucukkanat/bunker/internal/vendorlib"
	_ "github.com/kucukkanat/bunker/internal/web"
)
