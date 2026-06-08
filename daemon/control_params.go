package main

import (
	"encoding/json"

	"github.com/oines/astralops/pkg/protocol"
)

func controlParams(params any) json.RawMessage {
	raw, err := protocol.MarshalControlParams(params)
	if err != nil {
		panic(err)
	}
	return raw
}
