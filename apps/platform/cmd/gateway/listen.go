package main

import (
	"github.com/pion/webrtc/v4"
	"net/http"
)

type audioListener struct {
	id, owner string
	pc        *webrtc.PeerConnection
}

func (a *application) stopAudioListener(w http.ResponseWriter, r *http.Request) {
	value, ok := a.listeners.Load(r.PathValue("token"))
	if !ok {
		problem(w, 404, "监听已结束")
		return
	}
	listener := value.(*audioListener)
	if listener.owner != who(r).owner || listener.id != r.PathValue("listener") {
		problem(w, 404, "监听已结束")
		return
	}
	if a.listeners.CompareAndDelete(r.PathValue("token"), listener) {
		listener.pc.Close()
	}
	w.WriteHeader(204)
}
