package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	fData, fHeaders, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get file data", err)
		return
	}
	defer fData.Close()

	http.MaxBytesReader(w, fData, 1<<30)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video for video", videoID, "by user", userID)

	metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video Does Not Exist", err)
		return
	}

	if metadata.UserID != userID {
		respondWithError(w, http.StatusNotFound, "Not Video Owner", fmt.Errorf("Invalid User ID"))
		return
	}

	mediaType := fHeaders.Header.Get("Content-Type")
	mediaType, _, err = mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse media type", err)
		return
	}

	switch mediaType {
	case "video/mp4":
	default:
		respondWithError(w, http.StatusBadRequest, "Invalid Media Type", fmt.Errorf("Invalid Media Type"))
		return
	}

	videoType := strings.Split(mediaType, "/")
	random := make([]byte, 32)
	rand.Read(random)
	randFileName := base64.RawURLEncoding.EncodeToString(random)

	fileName := fmt.Sprintf("%s.%s", randFileName, videoType[1])

	file, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create file", err)
		return
	}
	defer os.Remove("tubely-upload.mp4")
	defer file.Close()

	_, err = io.Copy(file, fData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read file data", err)
		return
	}

	file.Seek(0, io.SeekStart)

	cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        file,
		ContentType: &mediaType,
	})

	url := fmt.Sprintf("http://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileName)

	metadata.VideoURL = &url

	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, metadata)
}
