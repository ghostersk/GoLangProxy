package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// StartServer launches a web server on 127.0.0.1:61147
func StartServer() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		indexPath := filepath.Join("www", "index.html")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			// Create index.html if it doesn’t exist
			if err := os.MkdirAll("www", 0755); err != nil {
				http.Error(w, "Error creating www directory", http.StatusInternalServerError)
				return
			}
			file, err := os.Create(indexPath)
			if err != nil {
				http.Error(w, "Error creating index.html", http.StatusInternalServerError)
				return
			}
			defer file.Close()
			_, err = file.WriteString("<h1>GoLangProxy is up</h1>")
			if err != nil {
				http.Error(w, "Error writing index.html", http.StatusInternalServerError)
				return
			}
		}
		http.ServeFile(w, r, indexPath)
	})

	fmt.Println("Starting simple web server on 127.0.0.1:61147")
	if err := http.ListenAndServe("127.0.0.1:61147", nil); err != nil {
		fmt.Println("Web server error:", err)
	}
}
