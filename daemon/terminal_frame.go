package main

import (
	internalterminal "github.com/oines/astralops/daemon/internal/core/terminal"
	"github.com/oines/astralops/pkg/protocol"
)

type terminalStreamFrame = internalterminal.StreamFrame
type terminalOpenParams = protocol.TerminalOpenParams
type terminalInputParams = protocol.TerminalInputParams
type terminalAttachParams = protocol.TerminalAttachParams
type terminalDetachParams = protocol.TerminalDetachParams
type terminalResizeParams = protocol.TerminalResizeParams
type terminalCloseParams = protocol.TerminalCloseParams
type terminalHeartbeatAckParams = protocol.TerminalHeartbeatAckParams
type terminalOpenResult = protocol.TerminalOpenResult
type terminalAckResult = protocol.TerminalAckResult
type terminalTab = protocol.TerminalTab
type terminalAttachResult = protocol.TerminalAttachResult

const (
	terminalFrameInput        = internalterminal.FrameInput
	terminalFrameResize       = internalterminal.FrameResize
	terminalFrameHeartbeatAck = internalterminal.FrameHeartbeatAck
	terminalFrameOutput       = internalterminal.FrameOutput
	terminalFrameHeartbeat    = internalterminal.FrameHeartbeat
	terminalFrameClosed       = internalterminal.FrameClosed
	terminalFrameError        = internalterminal.FrameError

	terminalStatusOpen             = "open"
	terminalStatusClosed           = "closed"
	terminalInputMaxBytes          = internalterminal.InputMaxBytes
	terminalOutputFrameMaxBytes    = internalterminal.OutputFrameMaxBytes
	terminalViewerAckTTL           = internalterminal.ViewerAckTTL
	terminalOutputDisconnectedCode = internalterminal.OutputDisconnectedCode
	terminalOutputDisconnectedText = internalterminal.OutputDisconnectedText
	terminalViewerRequiredCode     = internalterminal.ViewerRequiredCode
	terminalViewerNotReadyCode     = internalterminal.ViewerNotReadyCode
	terminalViewerMismatchCode     = internalterminal.ViewerMismatchCode
	defaultTerminalCols            = internalterminal.DefaultCols
	defaultTerminalRows            = internalterminal.DefaultRows
)
