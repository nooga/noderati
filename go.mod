module github.com/nooga/noderati

go 1.27.1

require (
	github.com/fsnotify/fsnotify v1.10.0
	github.com/nooga/paserati v0.0.0
	github.com/tetratelabs/wazero v1.12.0
	golang.org/x/net v0.56.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
)

require (
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/nooga/paserati => ../paserati

replace github.com/tetratelabs/wazero => github.com/nooga/wazero v1.12.1-0.20260911172836-0ec6142ae8c7
