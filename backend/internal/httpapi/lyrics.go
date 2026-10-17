package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"lyrics/internal/domain"
	"lyrics/internal/service"
)

type LyricsHandler struct {
	service *service.LyricsService
}

const contentTypeJSON = "application/json; charset=utf-8"

func NewLyricsHandler(service *service.LyricsService) *LyricsHandler {
	return &LyricsHandler{service: service}
}

func (handler *LyricsHandler) Get(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	lyrics, err := handler.service.Find(request.Context(), request.URL.Query().Get("artist"), request.URL.Query().Get("track"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrInvalidQuery) {
			status = http.StatusBadRequest
		} else if errors.Is(err, domain.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, lyrics)
}

func (handler *LyricsHandler) Search(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	results, err := handler.service.Search(request.Context(), request.URL.Query().Get("artist"), request.URL.Query().Get("track"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, results)
}

func (handler *LyricsHandler) Video(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	videoID, err := handler.service.FindVideo(request.Context(), request.URL.Query().Get("artist"), request.URL.Query().Get("track"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"videoId": videoID})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
