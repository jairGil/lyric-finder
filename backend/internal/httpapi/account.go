package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"lyrics/internal/auth"
	"lyrics/internal/domain"
	"lyrics/internal/service"
)

type AccountHandler struct {
	accounts *auth.Service
	library  *service.LibraryService
}

func NewAccountHandler(accounts *auth.Service, library *service.LibraryService) *AccountHandler {
	return &AccountHandler{accounts: accounts, library: library}
}

type credentialsRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

type trackRequest struct {
	ProviderID     int              `json:"providerId"`
	Artist         string           `json:"artist"`
	Title          string           `json:"title"`
	Album          string           `json:"album"`
	Duration       float64          `json:"duration"`
	YouTubeVideoID string           `json:"youtubeVideoId"`
	PlainLyrics    string           `json:"plainLyrics"`
	SyncedLyrics   []map[string]any `json:"syncedLyrics"`
}

func (handler *AccountHandler) Register(writer http.ResponseWriter, request *http.Request) {
	var input credentialsRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	user, token, err := handler.accounts.Register(request.Context(), input.Username, input.Email, input.Password)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"user": user, "token": token})
}

func (handler *AccountHandler) Login(writer http.ResponseWriter, request *http.Request) {
	var input credentialsRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	user, token, err := handler.accounts.Login(request.Context(), input.Login, input.Password)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"user": user, "token": token})
}

func (handler *AccountHandler) Logout(writer http.ResponseWriter, request *http.Request) {
	if err := handler.accounts.Logout(request.Context(), auth.TokenFromRequest(request)); err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *AccountHandler) Me(writer http.ResponseWriter, request *http.Request) {
	user, ok := auth.UserFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "autenticación requerida")
		return
	}
	writeJSON(writer, http.StatusOK, user)
}

func (handler *AccountHandler) Playlists(writer http.ResponseWriter, request *http.Request) {
	user, _ := auth.UserFromContext(request.Context())
	playlists, err := handler.library.ListPlaylists(request.Context(), user.ID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, playlists)
}

func (handler *AccountHandler) Library(writer http.ResponseWriter, request *http.Request) {
	user, _ := auth.UserFromContext(request.Context())
	collection := request.URL.Query().Get("collection")
	if collection == "" {
		collection = "saved"
	}
	if collection != "saved" && collection != "liked" && collection != "disliked" {
		writeError(writer, http.StatusBadRequest, "colección inválida")
		return
	}
	tracks, err := handler.library.ListTracks(request.Context(), user.ID, collection)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, tracks)
}

func (handler *AccountHandler) CreatePlaylist(writer http.ResponseWriter, request *http.Request) {
	user, _ := auth.UserFromContext(request.Context())
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	playlist, err := handler.library.CreatePlaylist(request.Context(), user.ID, input.Name)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, playlist)
}

func (handler *AccountHandler) RegisterTrack(writer http.ResponseWriter, request *http.Request) {
	var input trackRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	trackID, err := handler.library.RegisterTrack(request.Context(), domain.TrackInput{ProviderID: input.ProviderID, Artist: input.Artist, Title: input.Title, Album: input.Album, Duration: input.Duration, YouTubeVideoID: input.YouTubeVideoID, PlainLyrics: input.PlainLyrics, SyncedLyrics: input.SyncedLyrics})
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]int64{"trackId": trackID})
}

func (handler *AccountHandler) SaveTrack(writer http.ResponseWriter, request *http.Request) {
	user, _ := auth.UserFromContext(request.Context())
	trackID, err := pathID(request, "trackId")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Save bool `json:"save"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := handler.library.SaveTrack(request.Context(), user.ID, trackID, input.Save); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"saved": input.Save})
}

func (handler *AccountHandler) Reaction(writer http.ResponseWriter, request *http.Request) {
	user, _ := auth.UserFromContext(request.Context())
	trackID, err := pathID(request, "trackId")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Reaction int `json:"reaction"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := handler.library.SetReaction(request.Context(), user.ID, trackID, input.Reaction); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]int{"reaction": input.Reaction})
}

func (handler *AccountHandler) AddToPlaylist(writer http.ResponseWriter, request *http.Request) {
	user, _ := auth.UserFromContext(request.Context())
	playlistID, err := pathID(request, "playlistId")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		TrackID int64 `json:"trackId"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := handler.library.AddToPlaylist(request.Context(), user.ID, playlistID, input.TrackID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, domain.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		writeError(writer, status, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"status": "added"})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	if err := json.NewDecoder(request.Body).Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "JSON inválido")
		return false
	}
	return true
}

func pathID(request *http.Request, name string) (int64, error) {
	return strconv.ParseInt(request.PathValue(name), 10, 64)
}
