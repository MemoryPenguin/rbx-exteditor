package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

type script struct {
	FsPath     string // The path to the temporary file.
	Identifier string // The identifier passed by the plugin.
}

type context struct {
	Scripts       map[string]script   // All the scripts in the context.
	DirPath       string              // The path to the temporary folder the context is using.
	ScriptWatcher *fsnotify.Watcher   // The FS watcher watching script files.
	RbxEdits      map[string]struct{} // Scripts that have been edited from ROBLOX and should have the next FS change ignored.
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
		RbxEdits:      make(map[string]struct{}),
		DirPath:       dirPath,
		ScriptWatcher: watcher,
	}

	return ctx, nil
}

func destroyContext(ctx *context) {
	ctx.ScriptWatcher.Close()
	os.RemoveAll(ctx.DirPath)
}

func openFile(path string, editor string) error {
	cmd := exec.Command(editor, path)
	err := cmd.Start()

	if err != nil {
		return err
	}

	return nil
}

func main() {
	ctx, err := newContext()

	// Map of string in UUID
	changes := make(map[string]struct{})

	if err != nil {
		fmt.Printf("Unable to acquire context: %s\n", err)
	} else {
		defer destroyContext(ctx)

		interruptChannel := make(chan os.Signal)
		signal.Notify(interruptChannel, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-interruptChannel
			destroyContext(ctx)
			os.Exit(0)
		}()

		fmt.Printf("External edit agent has acquired context. Temporary files will be stored in %s.\n", ctx.DirPath)

		go func() {
			for {
				select {
				case event := <-ctx.ScriptWatcher.Events:
					if event.Op&fsnotify.Write == fsnotify.Write {
						uuid := strings.TrimSuffix(filepath.Base(event.Name), filepath.Ext(event.Name))
						log.Printf("%s was edited", uuid)

						if _, contains := ctx.RbxEdits[uuid]; !contains {
							changes[uuid] = struct{}{}
						} else {
							delete(ctx.RbxEdits, uuid)
						}
					}
				}
			}
		}()

		http.HandleFunc("/open", func(response http.ResponseWriter, request *http.Request) {
			uuid := request.PostFormValue("uuid")
			editorPath := request.PostFormValue("editor")

			if scr, ok := ctx.Scripts[uuid]; ok {
				log.Printf("Reopening UUID %s at FS path %s\n", uuid, scr.FsPath)
				fmt.Fprintf(response, "success: reopen")

				openFile(scr.FsPath, editorPath)
			} else {
				body := request.PostFormValue("body")
				scriptPath := path.Join(ctx.DirPath, uuid+".rbxs")

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

					openFile(scriptPath, editorPath)

					fmt.Printf("Opened UUID %s at FS path %s\n", uuid, scriptPath)
					fmt.Fprintf(response, "success: new")
				}
			}
		})

		http.HandleFunc("/changes", func(response http.ResponseWriter, request *http.Request) {
			realChanges := make(map[string]string)
			sentChanges := []string{}

			for uuid := range changes {
				scr := ctx.Scripts[uuid]
				body, err := ioutil.ReadFile(scr.FsPath)

				if err != nil {
					log.Printf("Couldn't read file: %s\n", err)
				} else {
					realChanges[uuid] = string(body)
					sentChanges = append(sentChanges, uuid)
				}
			}

			for _, sentUUID := range sentChanges {
				delete(changes, sentUUID)
			}

			encoded, _ := json.Marshal(realChanges)
			response.Write(encoded)
		})

		http.HandleFunc("/rbxedit", func(response http.ResponseWriter, request *http.Request) {
			uuid := request.PostFormValue("uuid")

			if scr, ok := ctx.Scripts[uuid]; ok {
				body := request.PostFormValue("body")
				ctx.RbxEdits[uuid] = struct{}{}

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
