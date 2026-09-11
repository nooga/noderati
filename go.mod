module github.com/nooga/noderati

go 1.26.0

require (
	github.com/nooga/paserati v0.0.0
	github.com/tetratelabs/wazero v1.12.0
	golang.org/x/net v0.56.0
	golang.org/x/term v0.45.0
)

require (
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/nooga/paserati => ../paserati

replace github.com/tetratelabs/wazero => github.com/nooga/wazero 9a771ab584919b1c19f653b1e1c48413bcfa1131
