package main

import "encoding/base64"

func remoteWriteParams(path string, body []byte) map[string]any {
	return map[string]any{"path": path, "dataBase64": base64.StdEncoding.EncodeToString(body)}
}

func remoteReadBytes(out map[string]any) ([]byte, error) {
	if data := stringValue(out["dataBase64"]); data != "" {
		return base64.StdEncoding.DecodeString(data)
	}
	return []byte(stringValue(out["content"])), nil
}
