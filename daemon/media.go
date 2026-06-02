package main

import (
	"fmt"
	"net/http"
	"strconv"
)

func (a *app) handleSessionMedia(w http.ResponseWriter, r *http.Request, sessionID, seqText, mediaID string) {
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid media reference"})
		return
	}
	media, err := a.mediaService().resolveSessionMedia(sessionID, seq, mediaID)
	if err != nil {
		writeActionError(w, err)
		return
	}
	if media.MIMEType != "" {
		w.Header().Set("Content-Type", media.MIMEType)
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", media.Name))
	}
	http.ServeFile(w, r, media.Path)
}
