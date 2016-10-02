@echo off
go build -o exteditor-win.exe
echo Windows build complete.
set GOOS=darwin
set GOARCH=amd64
go build -o exteditor-mac
echo Mac build complete.