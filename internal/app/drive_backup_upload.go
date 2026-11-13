package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const driveFileFields = "id,parents,trashed,size,sha256Checksum,md5Checksum"

type driveFile struct {
	ID      string   `json:"id"`
	Parents []string `json:"parents"`
	Trashed bool     `json:"trashed"`
	Size    int64    `json:"size,string"`
	SHA256  string   `json:"sha256Checksum"`
	MD5     string   `json:"md5Checksum"`
}

type driveUploadReceipt struct {
	ID, Checksum string
	Size         int64
}

func verifyDriveFile(ctx context.Context, client *http.Client, folder string, want driveUploadReceipt) error {
	if !driveID.MatchString(want.ID) {
		return errors.New("Google Drive did not return a file ID.")
	}
	q := url.Values{"fields": {driveFileFields}, "supportsAllDrives": {"true"}}
	var file driveFile
	if err := driveJSON(ctx, client, "GET", driveAPI+"/files/"+want.ID+"?"+q.Encode(), nil, &file); err != nil {
		return err
	}
	if file.ID != want.ID || file.Trashed || file.Size != want.Size || !slices.Contains(file.Parents, folder) {
		return errors.New("Drive file size or destination did not match. Source data was kept.")
	}
	sha, md, _ := strings.Cut(want.Checksum, ":")
	if (file.SHA256 != "" && strings.EqualFold(file.SHA256, sha)) || (file.SHA256 == "" && file.MD5 != "" && strings.EqualFold(file.MD5, md)) {
		return nil
	}
	if file.SHA256 != "" || file.MD5 != "" {
		return errors.New("Drive file checksum did not match. Source data was kept.")
	}
	// If Drive hasn't populated hashes yet, verify the downloaded bytes.
	request, err := http.NewRequestWithContext(ctx, "GET", driveAPI+"/files/"+want.ID+"?alt=media&supportsAllDrives=true", nil)
	if err != nil {
		return driveCallError(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return driveCallError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return driveStatusError(response)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(response.Body, want.Size+1))
	if err != nil || n != want.Size || hex.EncodeToString(hash.Sum(nil)) != sha {
		return errors.New("Drive file contents could not be verified. Source data was kept.")
	}
	return nil
}

func uploadDriveFile(ctx context.Context, client *http.Client, folder, name string, payload []byte) (driveUploadReceipt, error) {
	want := driveUploadReceipt{Size: int64(len(payload)), Checksum: fmt.Sprintf("%x:%x", sha256.Sum256(payload), md5.Sum(payload))}
	query := url.Values{"uploadType": {"resumable"}, "supportsAllDrives": {"true"}, "fields": {driveFileFields}}
	init, err := http.NewRequestWithContext(ctx, "POST", driveUploadAPI+"?"+query.Encode(), driveJSONBody(map[string]any{
		"name": name, "parents": []string{folder}, "mimeType": "application/gzip",
	}))
	if err != nil {
		return want, driveCallError(err)
	}
	init.Header.Set("Content-Type", "application/json")
	init.Header.Set("X-Upload-Content-Type", "application/gzip")
	init.Header.Set("X-Upload-Content-Length", strconv.Itoa(len(payload)))
	response, err := client.Do(init)
	if err != nil {
		return want, driveCallError(err)
	}
	if response.StatusCode != 200 && response.StatusCode != 201 {
		err = driveStatusError(response)
		response.Body.Close()
		return want, err
	}
	location := response.Header.Get("Location")
	response.Body.Close()
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "www.googleapis.com" && u.Host != "drive.googleapis.com") || u.Path != "/upload/drive/v3/files" {
		return want, errors.New("Google Drive did not return a valid upload session.")
	}
	const chunk = 4 << 20
	start, stalled := 0, 0
	probe := false
	for stalled < 4 && ctx.Err() == nil {
		end := min(start+chunk, len(payload))
		if start >= len(payload) {
			probe = true
		}
		var body *bytes.Reader
		var rangeHeader string
		if probe {
			body = bytes.NewReader(nil)
			rangeHeader = fmt.Sprintf("bytes */%d", len(payload))
		} else {
			body = bytes.NewReader(payload[start:end])
			rangeHeader = fmt.Sprintf("bytes %d-%d/%d", start, end-1, len(payload))
		}
		req, err := http.NewRequestWithContext(ctx, "PUT", location, body)
		if err != nil {
			return want, driveCallError(err)
		}
		req.Header.Set("Content-Type", "application/gzip")
		req.Header.Set("Content-Range", rangeHeader)
		resp, err := client.Do(req)
		if err != nil {
			// Recover ambiguous writes with a status probe.
			probe = true
			stalled++
			continue
		}
		switch {
		case resp.StatusCode == 200 || resp.StatusCode == 201:
			var file driveFile
			err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&file)
			resp.Body.Close()
			if err != nil || !driveID.MatchString(file.ID) {
				return want, errors.New("Google Drive did not confirm the uploaded file.")
			}
			want.ID = file.ID
			return want, verifyDriveFile(ctx, client, folder, want)
		case resp.StatusCode == 308:
			ack := resp.Header.Get("Range")
			resp.Body.Close()
			next := 0
			if ack != "" {
				last, ok := strings.CutPrefix(ack, "bytes=0-")
				n, err := strconv.Atoi(last)
				if !ok || err != nil || n < 0 || n >= len(payload) {
					return want, errors.New("Google Drive sent an invalid upload offset.")
				}
				next = n + 1
			}
			if next > start {
				stalled = 0
			} else {
				stalled++
			}
			start, probe = next, false
		case resp.StatusCode >= 500:
			resp.Body.Close()
			probe = true
			stalled++
		default:
			err = driveStatusError(resp)
			resp.Body.Close()
			return want, err
		}
	}
	return want, errors.New("Google Drive upload did not finish. Source data was kept.")
}
