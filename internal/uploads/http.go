package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yourusername/streamforge/internal/auth"
	"github.com/yourusername/streamforge/internal/queue"
	"github.com/yourusername/streamforge/internal/storage"
	"github.com/yourusername/streamforge/internal/videos"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type HTTPHandler struct {
	videos    videos.Repository
	storage   storage.Service
	queue     Publisher
	jobs      JobStore
	multipart storage.MultipartService
	sessions  MultipartSessionStore
}

type MultipartSessionStore interface {
	Create(context.Context, string, string, string, time.Time) error
	Part(context.Context, string, int, int64) error
	Complete(context.Context, string) error
	Abort(context.Context, string) error
}

type Publisher interface {
	Publish(context.Context, queue.Job) (string, error)
}

type JobStore interface {
	Create(context.Context, queue.Job) error
}

type initiateResponse struct {
	Upload storage.Upload `json:"upload"`
}

func NewHTTPHandler(videoRepository videos.Repository, storageService storage.Service, publisher Publisher, jobs ...JobStore) *HTTPHandler {
	handler := &HTTPHandler{videos: videoRepository, storage: storageService, queue: publisher}
	if multipart, ok := storageService.(storage.MultipartService); ok {
		handler.multipart = multipart
	}

	if len(jobs) > 0 {
		handler.jobs = jobs[0]
	}
	return handler
}

func (h *HTTPHandler) SetMultipartSessionStore(store MultipartSessionStore) {
	h.sessions = store
}

func (h *HTTPHandler) InitiateMultipart(w http.ResponseWriter, r *http.Request) {
	if h.multipart == nil {
		writeError(w, http.StatusNotImplemented, "multipart uploads are unavailable")
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	video, err := h.videos.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		writeVideoError(w, err)
		return
	}
	upload, err := h.multipart.InitiateMultipart(r.Context(), "videos/"+video.ID+"/original")
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage unavailable")
		return
	}
	if h.sessions != nil {
		if err := h.sessions.Create(r.Context(), video.ID, "videos/"+video.ID+"/original", upload.ID, upload.ExpiresAt); err != nil {
			writeError(w, http.StatusInternalServerError, "upload session persistence failed")
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"upload": upload})
}

func (h *HTTPHandler) UploadPart(w http.ResponseWriter, r *http.Request) {
	if h.multipart == nil {
		writeError(w, http.StatusNotImplemented, "multipart uploads are unavailable")
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	video, err := h.videos.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		writeVideoError(w, err)
		return
	}
	partNumber, err := strconv.Atoi(r.PathValue("part"))
	if err != nil || partNumber < 1 || partNumber > 10000 {
		writeError(w, http.StatusBadRequest, "part number must be between 1 and 10000")
		return
	}
	if r.ContentLength > 0 && r.ContentLength > 512<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "upload part is too large")
		return
	}
	body := http.MaxBytesReader(w, r.Body, 512<<20)
	data, err := io.ReadAll(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload part failed")
		return
	}
	if err := h.multipart.UploadPart(r.Context(), r.PathValue("uploadID"), "videos/"+video.ID+"/original", partNumber, bytes.NewReader(data)); err != nil {
		writeError(w, http.StatusBadRequest, "upload part failed")
		return
	}
	if h.sessions != nil {
		if err := h.sessions.Part(r.Context(), r.PathValue("uploadID"), partNumber, int64(len(data))); err != nil {
			writeError(w, http.StatusInternalServerError, "upload part persistence failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) CompleteMultipart(w http.ResponseWriter, r *http.Request) {
	if h.multipart == nil {
		writeError(w, http.StatusNotImplemented, "multipart uploads are unavailable")
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	video, err := h.videos.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		writeVideoError(w, err)
		return
	}
	var request struct {
		TotalParts int `json:"total_parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.TotalParts < 1 || request.TotalParts > 10000 {
		writeError(w, http.StatusBadRequest, "total_parts must be between 1 and 10000")
		return
	}
	object, err := h.multipart.CompleteMultipart(r.Context(), r.PathValue("uploadID"), "videos/"+video.ID+"/original", request.TotalParts)
	if err != nil {
		writeError(w, http.StatusConflict, "upload is not complete")
		return
	}
	h.finishUpload(w, r, video, object)
	if h.sessions != nil {
		_ = h.sessions.Complete(r.Context(), r.PathValue("uploadID"))
	}
}

func (h *HTTPHandler) AbortMultipart(w http.ResponseWriter, r *http.Request) {
	if h.multipart == nil {
		writeError(w, http.StatusNotImplemented, "multipart uploads are unavailable")
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if _, err := h.videos.GetForUser(user.ID, r.PathValue("id")); err != nil {
		writeVideoError(w, err)
		return
	}
	if err := h.multipart.AbortMultipart(r.Context(), r.PathValue("uploadID")); err != nil {
		writeError(w, http.StatusBadGateway, "storage unavailable")
		return
	}
	if h.sessions != nil {
		_ = h.sessions.Abort(r.Context(), r.PathValue("uploadID"))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	video, err := h.videos.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		writeVideoError(w, err)
		return
	}
	key := "videos/" + video.ID + "/original"
	upload, err := h.storage.Initiate(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage unavailable")
		return
	}
	writeJSON(w, http.StatusCreated, initiateResponse{Upload: upload})
}

func (h *HTTPHandler) Complete(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	videoID := r.PathValue("id")
	video, err := h.videos.GetForUser(user.ID, videoID)
	if err != nil {
		writeVideoError(w, err)
		return
	}
	key := "videos/" + video.ID + "/original"
	object, err := h.storage.Complete(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusConflict, "upload is not complete")
		return
	}
	h.finishUpload(w, r, video, object)
}

func (h *HTTPHandler) finishUpload(w http.ResponseWriter, r *http.Request, video videos.Video, object storage.Object) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var err error
	video, err = h.videos.MarkUploaded(user.ID, video.ID, object.Key, object.Size)
	if err != nil {
		writeVideoError(w, err)
		return
	}
	if h.queue != nil {
		job := queue.Job{ID: uuid.NewString(), VideoID: video.ID, InputKey: object.Key}
		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(r.Context(), carrier)
		job.TraceParent = carrier.Get("traceparent")
		job.TraceState = carrier.Get("tracestate")
		if h.jobs != nil {
			if err := h.jobs.Create(r.Context(), job); err != nil {
				writeError(w, http.StatusBadGateway, "processing job unavailable")
				return
			}
		}
		if _, err := h.queue.Publish(r.Context(), job); err != nil {
			writeError(w, http.StatusBadGateway, "processing queue unavailable")
			return
		}
	}
	writeJSON(w, http.StatusOK, video)
}

func (h *HTTPHandler) Abort(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	video, err := h.videos.GetForUser(user.ID, r.PathValue("id"))
	if err != nil {
		writeVideoError(w, err)
		return
	}
	if err := h.storage.Abort(r.Context(), "videos/"+video.ID+"/original"); err != nil {
		writeError(w, http.StatusBadGateway, "storage unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeVideoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, videos.ErrNotFound), errors.Is(err, videos.ErrNotOwner):
		writeError(w, http.StatusNotFound, "video not found")
	default:
		writeError(w, http.StatusBadRequest, "video operation failed")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
