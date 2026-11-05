package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
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
	defer os.Remove(file.Name())
	defer file.Close()

	_, err = io.Copy(file, fData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read file data", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(file.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get aspect ratio", err)
		return
	}
	prefix := ""
	switch aspectRatio {
	case "16:9":
		prefix = "landscape/"
	case "9:16":
		prefix = "portrait/"
	default:
		prefix = "other/"
	}
	fileName = prefix + fileName

	processedFilePath, err := processVideoForFastStart(file.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't process file for fast start", err)
		return
	}
	file.Close()
	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read processed file", err)
		return
	}
	defer os.Remove(processedFile.Name())
	defer processedFile.Close()

	cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        processedFile,
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

func getVideoAspectRatio(filePath string) (string, error) {
	getVideoData := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var buf []byte
	buffer := bytes.NewBuffer(buf)
	getVideoData.Stdout = buffer
	err := getVideoData.Run()
	if err != nil {
		return "", err
	}

	type videoJson struct {
		Streams []struct {
			Index              int    `json:"index"`
			CodecName          string `json:"codec_name"`
			CodecLongName      string `json:"codec_long_name"`
			CodecType          string `json:"codec_type"`
			CodecTagString     string `json:"codec_tag_string"`
			CodecTag           string `json:"codec_tag"`
			Width              int    `json:"width"`
			Height             int    `json:"height"`
			CodedWidth         int    `json:"coded_width"`
			CodedHeight        int    `json:"coded_height"`
			ClosedCaptions     int    `json:"closed_captions"`
			FilmGrain          int    `json:"film_grain"`
			HasBFrames         int    `json:"has_b_frames"`
			SampleAspectRatio  string `json:"sample_aspect_ratio"`
			DisplayAspectRatio string `json:"display_aspect_ratio"`
			PixFmt             string `json:"pix_fmt"`
			Level              int    `json:"level"`
			ColorRange         string `json:"color_range"`
			ColorSpace         string `json:"color_space"`
			Refs               int    `json:"refs"`
			RFrameRate         string `json:"r_frame_rate"`
			AvgFrameRate       string `json:"avg_frame_rate"`
			TimeBase           string `json:"time_base"`
			Disposition        struct {
				Default         int `json:"default"`
				Dub             int `json:"dub"`
				Original        int `json:"original"`
				Comment         int `json:"comment"`
				Lyrics          int `json:"lyrics"`
				Karaoke         int `json:"karaoke"`
				Forced          int `json:"forced"`
				HearingImpaired int `json:"hearing_impaired"`
				VisualImpaired  int `json:"visual_impaired"`
				CleanEffects    int `json:"clean_effects"`
				AttachedPic     int `json:"attached_pic"`
				TimedThumbnails int `json:"timed_thumbnails"`
				NonDiegetic     int `json:"non_diegetic"`
				Captions        int `json:"captions"`
				Descriptions    int `json:"descriptions"`
				Metadata        int `json:"metadata"`
				Dependent       int `json:"dependent"`
				StillImage      int `json:"still_image"`
			} `json:"disposition"`
		} `json:"streams"`
	}

	var video videoJson
	if err := json.Unmarshal(buffer.Bytes(), &video); err != nil {
		log.Fatal(err)
	}

	aspectRatio := video.Streams[0].Width / video.Streams[0].Height

	switch aspectRatio {
	case 1:
		return "16:9", nil
	case 0:
		return "9:16", nil
	default:
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"
	processVideo := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	err := processVideo.Run()
	if err != nil {
		return "", err
	}
	return outputFilePath, nil
}
