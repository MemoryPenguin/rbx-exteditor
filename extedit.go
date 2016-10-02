package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"

	"github.com/fsnotify/fsnotify"
)

type script struct {
	FsPath     string // The path to the temporary file.
	Identifier string // The identifier passed by the plugin.
}

type context struct {
	Scripts       map[string]script // All the scripts in the context.
	DirPath       string            // The path to the temporary folder the context is using.
	ScriptWatcher *fsnotify.Watcher // The FS watcher watching script files.
	RbxEdits      map[string]bool   // Scripts that have been edited from ROBLOX and should have the next FS change ignored.
}

func newContext() (*context, error) {
	dirPath, err := ioutil.TempDir("", "extedit")

	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx := &context{
		Scripts:       make(map[string]script),
		RbxEdits:      make(map[string]bool),
		DirPath:       dirPath,
		ScriptWatcher: watcher,
	}

	return ctx, nil
}

func destroyContext(ctx *context) {
	ctx.ScriptWatcher.Close()
	os.RemoveAll(ctx.DirPath)
}

func handler(response http.ResponseWriter, request *http.Request) {
	fmt.Fprintf(response, "Hi %s", request.URL.Path)
}

func main() {
	ctx, err := newContext()

	// Map of string in UUID / string form
	changes := make(map[string]string)

	if err != nil {
		fmt.Printf("Unable to acquire context: %s\n", err)
	} else {
		defer destroyContext(ctx)

		fmt.Printf("External edit agent has acquired context. Temporary files will be stored in %s.\n", ctx.DirPath)

		go func() {
			for {
				select {
				case event := <-ctx.ScriptWatcher.Events:
					if event.Op&fsnotify.Write == fsnotify.Write {
						log.Printf("Edit to %s\n", event.Name)

						uuid := path.Base(event.Name)
						log.Printf("Edit to %s", uuid)
					}
				}
			}
		}()

		http.HandleFunc("/open", func(response http.ResponseWriter, request *http.Request) {
			uuid := request.PostFormValue("uuid")

			if _, ok := ctx.Scripts[uuid]; ok {
				fmt.Fprint(response, "failure: already registered\n")
			} else {
				body := request.PostFormValue("body")
				scriptPath := path.Join(ctx.DirPath, uuid, ".rbxs")

				err := ioutil.WriteFile(scriptPath, []byte(body), 0644)

				if err != nil {
					fmt.Fprintf(response, "failure: error writing: %s\n", err)
					log.Fatalf("Error writing to file: %s\n", err)
				} else {
					scr := script{
						FsPath:     scriptPath,
						Identifier: uuid,
					}

					ctx.Scripts[uuid] = scr
					ctx.ScriptWatcher.Add(scriptPath)

					cmd := exec.Command(request.PostFormValue("editor"), scriptPath)
					err := cmd.Start()

					if err != nil {
						log.Fatal(err)
					}

					fmt.Printf("Opened UUID %s at FS path %s\n", uuid, scriptPath)
				}
			}
		})

		http.HandleFunc("/changes", func(response http.ResponseWriter, request *http.Request) {
			encoded, _ := json.Marshal(changes)
			fmt.Printf(string(encoded))
			response.Write(encoded)
		})

		http.HandleFunc("/rbxedit", func(response http.ResponseWriter, request *http.Request) {
			uuid := request.PostFormValue("uuid")

			if scr, ok := ctx.Scripts[uuid]; ok {
				body := request.PostFormValue("body")
				ctx.RbxEdits[uuid] = true

				err := ioutil.WriteFile(scr.FsPath, []byte(body), 0644)

				if err != nil {
					fmt.Fprintf(response, "failure: error writing: %s\n", err)
					log.Fatalf("Error writing to file: %s\n", err)
				}
			} else {
				fmt.Printf("Got rbx edit for unopened UUID %s\n", uuid)
				fmt.Fprintf(response, "failure: %s is not opened by this host", uuid)
			}
		})

		http.ListenAndServe("localhost:8080", nil)
	}
}
