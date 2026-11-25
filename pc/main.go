// The PC app serves the same frontend the phone uses and opens it in a window.
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	root := os.Getenv("VECTRABOT_FRONTEND")
	if root == "" {
		root = filepath.Join("..", "frontend")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	go func() {
		log.Fatal(http.Serve(listener, http.FileServer(http.Dir(root))))
	}()
	command, args := openCommand(url)
	if err := exec.Command(command, args...).Start(); err != nil {
		log.Fatal(err)
	}
	log.Printf("Vectrabot PC app at %s", url)
	select {}
}

func openCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
