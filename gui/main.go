package main

import (
	"embed" // erlaubt das Einbetten von Dateien ins Binary

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// GO-EINSTEIGER: Das ist eine "compiler directive" (Magie-Kommentar OHNE Leerzeichen
// nach //). //go:embed bettet die Dateien in das kompilierte Binary ein, sodass die
// GUI das fertige Frontend (HTML/JS/CSS) mit sich trägt — eine einzige verteilbare
// Datei, keine externen Assets nötig. `all:` schließt auch versteckte Dateien ein.
//
//go:embed all:frontend/dist
var assets embed.FS // FS = ein eingebettetes Dateisystem

func main() {
	// App-Struktur erzeugen (siehe app.go).
	app := NewApp()

	// Wails-Anwendung mit Optionen starten. &options.App{...} ist ein Pointer auf
	// ein Options-Struct mit benannten Feldern.
	err := wails.Run(&options.App{
		Title:     "Kilometrix — Backend",
		Width:     980,
		Height:    760,
		MinWidth:  720,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: assets, // das oben eingebettete Frontend ausliefern
		},
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 246, A: 1}, // helle Taskpane-Fläche
		OnStartup:        app.startup,                                  // Callback: Wails übergibt den Context
		// Bind macht die exportierten Methoden von `app` im Frontend als
		// JS-Funktionen aufrufbar. []interface{} ist ein Slice beliebiger Werte.
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		// println ist ein eingebautes Mini-Print (schreibt nach stderr). In echtem
		// Code nimmt man eher fmt/log; hier reicht es für den Startfehler.
		println("Error:", err.Error())
	}
}
