module wick_go

go 1.24.0

require wick_server v0.0.0

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace wick_server => ../wick_deep_agent/server
