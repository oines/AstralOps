package main

import "github.com/oines/astralops/pkg/protocol"

func eventNormalized(kind any, value any) protocol.AstralEventNormalized {
	return protocol.EventNormalized(kind, value)
}
