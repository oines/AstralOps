package main

import "github.com/oines/astralops/pkg/relaymesh"

type RelayClient = relaymesh.Client
type RelayWebSocketConn = relaymesh.WebSocketConn
type relayEnvelopeAckInput = relaymesh.EnvelopeAckInput

func relayEnvelopeAckAlreadyConsumed(err error) bool {
	return relaymesh.EnvelopeAckAlreadyConsumed(err)
}

func relayWebSocketURL(baseURL, deviceID string) (string, error) {
	return relaymesh.WebSocketURL(baseURL, deviceID)
}
