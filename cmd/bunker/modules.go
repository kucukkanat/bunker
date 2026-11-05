package main

// Feature modules register themselves in init(). Add one blank import per module.
import (
	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/notify"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/tlsauto"
	_ "github.com/kucukkanat/bunker/internal/web"
)
