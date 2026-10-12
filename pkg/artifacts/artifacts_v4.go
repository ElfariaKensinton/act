// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package artifacts

// GitHub Actions Artifacts V4 API Simple Description
//
// 1. Upload artifact
// 1.1. CreateArtifact
// Post: /twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact
// Request:
// {
//     "workflow_run_backend_id": "21",
//     "workflow_job_run_backend_id": "49",
//     "name": "test",
//     "version": 4
// }
// Response:
// {
//     "ok": true,
//     "signedUploadUrl": "http://localhost:3000/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact?sig=mO7y35r4GyjN7fwg0DTv3-Fv1NDXD84KLEgLpoPOtDI=&expires=2024-01-23+21%3A48%3A37.20833956+%2B0100+CET&artifactName=test&taskID=75"
// }
// 1.2. Upload Zip Content to Blobstorage (unauthenticated request)
// PUT: http://localhost:3000/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact?sig=mO7y35r4GyjN7fwg0DTv3-Fv1NDXD84KLEgLpoPOtDI=&expires=2024-01-23+21%3A48%3A37.20833956+%2B0100+CET&artifactName=test&taskID=75&comp=block
// 1.3. Continue Upload Zip Content to Blobstorage (unauthenticated request), repeat until everything is uploaded
// PUT: http://localhost:3000/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact?sig=mO7y35r4GyjN7fwg0DTv3-Fv1NDXD84KLEgLpoPOtDI=&expires=2024-01-23+21%3A48%3A37.20833956+%2B0100+CET&artifactName=test&taskID=75&comp=appendBlock
// 1.4. Unknown xml payload to Blobstorage (unauthenticated request), ignored for now
// PUT: http://localhost:3000/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact?sig=mO7y35r4GyjN7fwg0DTv3-Fv1NDXD84KLEgLpoPOtDI=&expires=2024-01-23+21%3A48%3A37.20833956+%2B0100+CET&artifactName=test&taskID=75&comp=blockList
// 1.5. FinalizeArtifact
// Post: /twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact
// Request
// {
//     "workflow_run_backend_id": "21",
//     "workflow_job_run_backend_id": "49",
//     "name": "test",
//     "size": "2097",
//     "hash": "sha256:b6325614d5649338b87215d9536b3c0477729b8638994c74cdefacb020a2cad4"
// }
// Response
// {
//     "ok": true,
//     "artifactId": "4"
// }
// 2. Download artifact
// 2.1. ListArtifacts and optionally filter by artifact exact name or id
// Post: /twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts
// Request
// {
//     "workflow_run_backend_id": "21",
//     "workflow_job_run_backend_id": "49",
//     "name_filter": "test"
// }
// Response
// {
//     "artifacts": [
//         {
//             "workflowRunBackendId": "21",
//             "workflowJobRunBackendId": "49",
//             "databaseId": "4",
//             "name": "test",
//             "size": "2093",
//             "createdAt": "2024-01-23T00:13:28Z"
//         }
//     ]
// }
// 2.2. GetSignedArtifactURL get the URL to download the artifact zip file of a specific artifact
// Post: /twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL
// Request
// {
//     "workflow_run_backend_id": "21",
//     "workflow_job_run_backend_id": "49",
//     "name": "test"
// }
// Response
// {
//     "signedUrl": "http://localhost:3000/twirp/github.actions.results.api.v1.ArtifactService/DownloadArtifact?sig=wHzFOwpF-6220-5CA0CIRmAX9VbiTC2Mji89UOqo1E8=&expires=2024-01-23+21%3A51%3A56.872846295+%2B0100+CET&artifactName=test&taskID=76"
// }
// 2.3. Download Zip from Blobstorage (unauthenticated request)
// GET: http://localhost:3000/twirp/github.actions.results.api.v1.ArtifactService/DownloadArtifact?sig=wHzFOwpF-6220-5CA0CIRmAX9VbiTC2Mji89UOqo1E8=&expires=2024-01-23+21%3A51%3A56.872846295+%2B0100+CET&artifactName=test&taskID=76

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	ArtifactV4RouteBase       = "/twirp/github.actions.results.api.v1.ArtifactService"
	ArtifactV4ContentEncoding = "application/zip"
)

type artifactV4Routes struct {
	prefix  string
	fs      WriteFS
	rfs     fs.FS
	AppURL  string
	baseDir string
}

type ArtifactContext struct {
	Req  *http.Request
	Resp http.ResponseWriter
}

func artifactNameToID(s string) int64 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int64(h.Sum32())
}

func (c ArtifactContext) Error(status int, _ ...interface{}) {
	c.Resp.WriteHeader(status)
}

func (c ArtifactContext) JSON(status int, _ ...interface{}) {
	c.Resp.WriteHeader(status)
}

func validateRunIDV4(ctx *ArtifactContext, rawRunID string) (interface{}, int64, bool) {
	// The Results API sends workflow_run_backend_id as a string. GitHub-hosted
	// IDs are often numeric, but compatible services may use opaque IDs (for
	// example UUIDs). Preserve the legacy numeric directory layout and map
	// opaque IDs to a stable, filesystem-safe numeric key.
	if rawRunID == "" {
		log.Error("Error runID is empty")
		ctx.Error(http.StatusBadRequest, "workflow_run_backend_id is required")
		return nil, 0, false
	}
	runID, err := strconv.ParseInt(rawRunID, 10, 64)
	if err != nil {
		h := fnv.New64a()
		_, _ = h.Write([]byte(rawRunID))
		runID = int64(h.Sum64())
	}
	return nil, runID, true
}

func RoutesV4(router *httprouter.Router, baseDir string, fsys WriteFS, rfs fs.FS) {
	route := &artifactV4Routes{
		fs:      fsys,
		rfs:     rfs,
		baseDir: baseDir,
		prefix:  ArtifactV4RouteBase,
	}
	router.POST(path.Join(ArtifactV4RouteBase, "CreateArtifact"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.AppURL = requestBaseURL(r)
		route.createArtifact(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
	router.POST(path.Join(ArtifactV4RouteBase, "FinalizeArtifact"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.finalizeArtifact(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
	router.POST(path.Join(ArtifactV4RouteBase, "ListArtifacts"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.listArtifacts(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
	router.POST(path.Join(ArtifactV4RouteBase, "GetSignedArtifactURL"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.AppURL = requestBaseURL(r)
		route.getSignedArtifactURL(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
	router.POST(path.Join(ArtifactV4RouteBase, "DeleteArtifact"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.AppURL = requestBaseURL(r)
		route.deleteArtifact(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
	router.PUT(path.Join(ArtifactV4RouteBase, "UploadArtifact"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.uploadArtifact(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
	router.GET(path.Join(ArtifactV4RouteBase, "DownloadArtifact"), func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		route.downloadArtifact(&ArtifactContext{
			Req:  r,
			Resp: w,
		})
	})
}

func (r artifactV4Routes) buildSignature(endp, expires, artifactName string, taskID int64) []byte {
	mac := hmac.New(sha256.New, []byte{0xba, 0xdb, 0xee, 0xf0})
	mac.Write([]byte(endp))
	mac.Write([]byte(expires))
	mac.Write([]byte(artifactName))
	mac.Write([]byte(fmt.Sprint(taskID)))
	return mac.Sum(nil)
}

func (r artifactV4Routes) buildArtifactURL(endp, artifactName string, taskID int64) string {
	expires := time.Now().Add(60 * time.Minute).Format("2006-01-02 15:04:05.999999999 -0700 MST")
	baseURL := strings.TrimSuffix(r.AppURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost"
	}
	return baseURL + strings.TrimSuffix(r.prefix, "/") +
		"/" + endp + "?sig=" + base64.URLEncoding.EncodeToString(r.buildSignature(endp, expires, artifactName, taskID)) + "&expires=" + url.QueryEscape(expires) + "&artifactName=" + url.QueryEscape(artifactName) + "&taskID=" + fmt.Sprint(taskID)
}

func (r artifactV4Routes) verifySignature(ctx *ArtifactContext, endp string) (int64, string, bool) {
	rawTaskID := ctx.Req.URL.Query().Get("taskID")
	sig := ctx.Req.URL.Query().Get("sig")
	expires := ctx.Req.URL.Query().Get("expires")
	artifactName := ctx.Req.URL.Query().Get("artifactName")
	dsig, _ := base64.URLEncoding.DecodeString(sig)
	taskID, _ := strconv.ParseInt(rawTaskID, 10, 64)

	expecedsig := r.buildSignature(endp, expires, artifactName, taskID)
	if !hmac.Equal(dsig, expecedsig) {
		log.Error("Error unauthorized")
		ctx.Error(http.StatusUnauthorized, "Error unauthorized")
		return -1, "", false
	}
	t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", expires)
	if err != nil || t.Before(time.Now()) {
		log.Error("Error link expired")
		ctx.Error(http.StatusUnauthorized, "Error link expired")
		return -1, "", false
	}
	return taskID, artifactName, true
}

func (r *artifactV4Routes) parseProtbufBody(ctx *ArtifactContext, req protoreflect.ProtoMessage) bool {
	body, err := io.ReadAll(ctx.Req.Body)
	if err != nil {
		log.Errorf("Error decode request body: %v", err)
		ctx.Error(http.StatusInternalServerError, "Error decode request body")
		return false
	}
	err = protojson.Unmarshal(body, req)
	if err != nil {
		log.Errorf("Error decode request body: %v", err)
		ctx.Error(http.StatusInternalServerError, "Error decode request body")
		return false
	}
	return true
}

func (r *artifactV4Routes) sendProtbufBody(ctx *ArtifactContext, req protoreflect.ProtoMessage) {
	resp, err := protojson.Marshal(req)
	if err != nil {
		log.Errorf("Error encode response body: %v", err)
		ctx.Error(http.StatusInternalServerError, "Error encode response body")
		return
	}
	ctx.Resp.Header().Set("Content-Type", "application/json;charset=utf-8")
	ctx.Resp.WriteHeader(http.StatusOK)
	_, _ = ctx.Resp.Write(resp)
}

func (r *artifactV4Routes) createArtifact(ctx *ArtifactContext) {
	var req CreateArtifactRequest

	if ok := r.parseProtbufBody(ctx, &req); !ok {
		return
	}
	_, runID, ok := validateRunIDV4(ctx, req.WorkflowRunBackendId)
	if !ok {
		return
	}

	artifactName := req.Name

	safeRunPath := safeResolve(r.baseDir, fmt.Sprint(runID))
	safePath := safeResolve(safeRunPath, artifactName)
	safePath = safeResolve(safePath, artifactName+".zip")
	file, err := r.fs.OpenWritable(safePath)
	if err != nil {
		log.Errorf("CreateArtifact could not prepare storage: run_id=%q artifact=%q path=%q: %v", req.WorkflowRunBackendId, artifactName, safePath, err)
		ctx.Error(http.StatusInternalServerError, "could not prepare artifact storage")
		return
	}
	if err := file.Close(); err != nil {
		log.Errorf("CreateArtifact could not close storage: run_id=%q artifact=%q path=%q: %v", req.WorkflowRunBackendId, artifactName, safePath, err)
		ctx.Error(http.StatusInternalServerError, "could not prepare artifact storage")
		return
	}
	log.Infof("Artifact upload initialized: run_id=%q artifact=%q path=%q", req.WorkflowRunBackendId, artifactName, safePath)

	respData := CreateArtifactResponse{
		Ok:              true,
		SignedUploadUrl: r.buildArtifactURL("UploadArtifact", artifactName, runID),
	}
	r.sendProtbufBody(ctx, &respData)
}

type artifactBlockListXML struct {
	IDs []string
}

// UnmarshalXML keeps the order of IDs from Azure's block-list document. Actions
// artifacts can retry a block upload, so each block must be staged by block ID
// and assembled only when the block list is committed.
func (b *artifactBlockListXML) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch item := token.(type) {
		case xml.StartElement:
			switch item.Name.Local {
			case "Latest", "Uncommitted", "Committed":
				var id string
				if err := decoder.DecodeElement(&id, &item); err != nil {
					return err
				}
				if id = strings.TrimSpace(id); id != "" {
					b.IDs = append(b.IDs, id)
				}
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if item.Name == start.Name {
				return nil
			}
		}
	}
}

func artifactV4ZipPath(baseDir string, taskID int64, artifactName string) string {
	runPath := safeResolve(baseDir, fmt.Sprint(taskID))
	artifactPath := safeResolve(runPath, artifactName)
	return safeResolve(artifactPath, artifactName+".zip")
}

func artifactV4BlockDir(baseDir string, taskID int64, artifactName string) string {
	runPath := safeResolve(baseDir, fmt.Sprint(taskID))
	artifactPath := safeResolve(runPath, artifactName)
	return safeResolve(artifactPath, ".blocks")
}

func artifactV4BlockPath(baseDir string, taskID int64, artifactName, blockID string) string {
	// Hash the opaque block ID rather than using it as a filesystem path.
	sum := sha256.Sum256([]byte(blockID))
	return safeResolve(artifactV4BlockDir(baseDir, taskID, artifactName), hex.EncodeToString(sum[:])+".block")
}

type artifactRemover interface {
	RemoveAll(string) error
}

func (r *artifactV4Routes) commitBlockList(ctx *ArtifactContext, taskID int64, artifactName string, blockIDs []string) {
	if len(blockIDs) == 0 {
		log.Errorf("Artifact block list is empty: task=%d artifact=%q", taskID, artifactName)
		ctx.Error(http.StatusBadRequest, "empty artifact block list")
		return
	}

	blockFiles := make([]fs.File, 0, len(blockIDs))
	for _, blockID := range blockIDs {
		blockPath := artifactV4BlockPath(r.baseDir, taskID, artifactName, blockID)
		file, err := r.rfs.Open(blockPath)
		if err != nil {
			for _, opened := range blockFiles {
				_ = opened.Close()
			}
			log.Errorf("Artifact block missing: task=%d artifact=%q block_id=%q: %v", taskID, artifactName, blockID, err)
			ctx.Error(http.StatusBadRequest, "artifact block missing")
			return
		}
		blockFiles = append(blockFiles, file)
	}
	defer func() {
		for _, file := range blockFiles {
			_ = file.Close()
		}
	}()

	zipPath := artifactV4ZipPath(r.baseDir, taskID, artifactName)
	output, err := r.fs.OpenWritable(zipPath)
	if err != nil {
		log.Errorf("Artifact block commit could not prepare file: task=%d artifact=%q: %v", taskID, artifactName, err)
		ctx.Error(http.StatusInternalServerError, "could not prepare artifact file")
		return
	}

	var total int64
	for i, block := range blockFiles {
		n, copyErr := io.Copy(output, block)
		total += n
		if copyErr != nil {
			_ = output.Close()
			log.Errorf("Artifact block commit failed: task=%d artifact=%q block_index=%d: %v", taskID, artifactName, i, copyErr)
			ctx.Error(http.StatusInternalServerError, "artifact block commit failed")
			return
		}
	}
	if err := output.Close(); err != nil {
		log.Errorf("Artifact block commit close failed: task=%d artifact=%q: %v", taskID, artifactName, err)
		ctx.Error(http.StatusInternalServerError, "artifact block commit failed")
		return
	}

	if remover, ok := r.fs.(artifactRemover); ok {
		_ = remover.RemoveAll(artifactV4BlockDir(r.baseDir, taskID, artifactName))
	}
	log.Infof("Artifact upload blocks committed: task=%d artifact=%q blocks=%d bytes=%d", taskID, artifactName, len(blockIDs), total)
	ctx.JSON(http.StatusCreated, "created")
}

func (r *artifactV4Routes) uploadArtifact(ctx *ArtifactContext) {
	task, artifactName, ok := r.verifySignature(ctx, "UploadArtifact")
	if !ok {
		return
	}

	comp := strings.ToLower(ctx.Req.URL.Query().Get("comp"))
	switch comp {
	case "block":
		blockID := ctx.Req.URL.Query().Get("blockid")
		if blockID == "" {
			ctx.Error(http.StatusBadRequest, "blockid is required")
			return
		}
		// A block ID is an idempotency key. Repeated uploads of the same block
		// must replace it, not append the retried body after a partial upload.
		blockPath := artifactV4BlockPath(r.baseDir, task, artifactName, blockID)
		file, err := r.fs.OpenWritable(blockPath)
		if err != nil {
			log.Errorf("Artifact block upload could not prepare storage: task=%d artifact=%q: %v", task, artifactName, err)
			ctx.Error(http.StatusInternalServerError, "could not prepare artifact block")
			return
		}
		if ctx.Req.Body == nil {
			_ = file.Close()
			ctx.Error(http.StatusBadRequest, "no body given")
			return
		}
		n, copyErr := io.Copy(file, ctx.Req.Body)
		closeErr := file.Close()
		if copyErr != nil {
			log.Errorf("Artifact block upload failed: task=%d artifact=%q comp=%q block_id=%q bytes_received=%d content_length=%d remote=%q user_agent=%q: %v",
				task, artifactName, comp, blockID, n, ctx.Req.ContentLength, ctx.Req.RemoteAddr, ctx.Req.UserAgent(), copyErr)
			ctx.Error(http.StatusInternalServerError, "artifact block upload failed")
			return
		}
		if closeErr != nil {
			log.Errorf("Artifact block upload close failed: task=%d artifact=%q block_id=%q: %v", task, artifactName, blockID, closeErr)
			ctx.Error(http.StatusInternalServerError, "artifact block upload failed")
			return
		}
		log.Infof("Artifact block stored: task=%d artifact=%q block_id=%q bytes=%d content_length=%d",
			task, artifactName, blockID, n, ctx.Req.ContentLength)
		ctx.JSON(http.StatusCreated, "appended")
	case "appendblock":
		// Retain support for clients using append-block semantics.
		zipPath := artifactV4ZipPath(r.baseDir, task, artifactName)
		file, err := r.fs.OpenAppendable(zipPath)
		if err != nil {
			log.Errorf("Artifact append-block upload could not prepare storage: task=%d artifact=%q: %v", task, artifactName, err)
			ctx.Error(http.StatusInternalServerError, "could not prepare artifact file")
			return
		}
		if ctx.Req.Body == nil {
			_ = file.Close()
			ctx.Error(http.StatusBadRequest, "no body given")
			return
		}
		n, copyErr := io.Copy(file, ctx.Req.Body)
		closeErr := file.Close()
		if copyErr != nil {
			log.Errorf("Artifact append-block upload failed: task=%d artifact=%q bytes_received=%d content_length=%d remote=%q: %v",
				task, artifactName, n, ctx.Req.ContentLength, ctx.Req.RemoteAddr, copyErr)
			ctx.Error(http.StatusInternalServerError, "artifact append-block upload failed")
			return
		}
		if closeErr != nil {
			log.Errorf("Artifact append-block close failed: task=%d artifact=%q: %v", task, artifactName, closeErr)
			ctx.Error(http.StatusInternalServerError, "artifact append-block upload failed")
			return
		}
		log.Infof("Artifact append block stored: task=%d artifact=%q bytes=%d", task, artifactName, n)
		ctx.JSON(http.StatusCreated, "appended")
	case "blocklist":
		body, err := io.ReadAll(ctx.Req.Body)
		if err != nil {
			log.Errorf("Artifact block list read failed: task=%d artifact=%q: %v", task, artifactName, err)
			ctx.Error(http.StatusInternalServerError, "could not read artifact block list")
			return
		}
		var blockList artifactBlockListXML
		if err := xml.Unmarshal(body, &blockList); err != nil {
			log.Errorf("Artifact block list decode failed: task=%d artifact=%q: %v", task, artifactName, err)
			ctx.Error(http.StatusBadRequest, "invalid artifact block list")
			return
		}
		r.commitBlockList(ctx, task, artifactName, blockList.IDs)
	default:
		log.Errorf("Unsupported artifact upload operation: comp=%q path=%s", ctx.Req.URL.Query().Get("comp"), ctx.Req.URL.Path)
		ctx.Error(http.StatusBadRequest, "unsupported artifact upload operation")
	}
}

func (r *artifactV4Routes) finalizeArtifact(ctx *ArtifactContext) {
	var req FinalizeArtifactRequest

	if ok := r.parseProtbufBody(ctx, &req); !ok {
		return
	}
	_, runID, ok := validateRunIDV4(ctx, req.WorkflowRunBackendId)
	if !ok {
		return
	}

	safeRunPath := safeResolve(r.baseDir, fmt.Sprint(runID))
	safePath := safeResolve(safeRunPath, req.Name)
	safePath = safeResolve(safePath, req.Name+".zip")
	file, err := r.rfs.Open(safePath)
	if err != nil {
		log.Errorf("Artifact finalize failed: run_id=%q artifact=%q path=%q: %v", req.WorkflowRunBackendId, req.Name, safePath, err)
		ctx.Error(http.StatusNotFound, "uploaded artifact file not found")
		return
	}
	info, statErr := file.Stat()
	_ = file.Close()
	if statErr != nil {
		log.Errorf("Artifact finalize could not stat file: run_id=%q artifact=%q path=%q: %v", req.WorkflowRunBackendId, req.Name, safePath, statErr)
	} else {
		log.Infof("Artifact upload finalized: run_id=%q artifact=%q stored_bytes=%d declared_size=%d", req.WorkflowRunBackendId, req.Name, info.Size(), req.Size)
	}

	respData := FinalizeArtifactResponse{
		Ok:         true,
		ArtifactId: artifactNameToID(req.Name),
	}
	r.sendProtbufBody(ctx, &respData)
}
func (r *artifactV4Routes) listArtifacts(ctx *ArtifactContext) {
	var req ListArtifactsRequest

	if ok := r.parseProtbufBody(ctx, &req); !ok {
		return
	}
	_, runID, ok := validateRunIDV4(ctx, req.WorkflowRunBackendId)
	if !ok {
		return
	}

	safePath := safeResolve(r.baseDir, fmt.Sprint(runID))

	entries, err := fs.ReadDir(r.rfs, safePath)
	if err != nil {
		panic(err)
	}

	list := []*ListArtifactsResponse_MonolithArtifact{}

	for _, entry := range entries {
		id := artifactNameToID(entry.Name())
		if (req.NameFilter == nil || req.NameFilter.Value == entry.Name()) && (req.IdFilter == nil || req.IdFilter.Value == id) {
			data := &ListArtifactsResponse_MonolithArtifact{
				Name:                    entry.Name(),
				CreatedAt:               timestamppb.Now(),
				DatabaseId:              id,
				WorkflowRunBackendId:    req.WorkflowRunBackendId,
				WorkflowJobRunBackendId: req.WorkflowJobRunBackendId,
				Size:                    0,
			}
			if info, err := entry.Info(); err == nil {
				data.Size = info.Size()
				data.CreatedAt = timestamppb.New(info.ModTime())
			}
			list = append(list, data)
		}
	}

	respData := ListArtifactsResponse{
		Artifacts: list,
	}
	r.sendProtbufBody(ctx, &respData)
}

func (r *artifactV4Routes) getSignedArtifactURL(ctx *ArtifactContext) {
	var req GetSignedArtifactURLRequest

	if ok := r.parseProtbufBody(ctx, &req); !ok {
		return
	}
	_, runID, ok := validateRunIDV4(ctx, req.WorkflowRunBackendId)
	if !ok {
		return
	}

	artifactName := req.Name

	respData := GetSignedArtifactURLResponse{}

	respData.SignedUrl = r.buildArtifactURL("DownloadArtifact", artifactName, runID)
	r.sendProtbufBody(ctx, &respData)
}

func (r *artifactV4Routes) downloadArtifact(ctx *ArtifactContext) {
	task, artifactName, ok := r.verifySignature(ctx, "DownloadArtifact")
	if !ok {
		return
	}

	safeRunPath := safeResolve(r.baseDir, fmt.Sprint(task))
	safePath := safeResolve(safeRunPath, artifactName)
	safePath = safeResolve(safePath, artifactName+".zip")

	file, _ := r.rfs.Open(safePath)

	_, _ = io.Copy(ctx.Resp, file)
}

func (r *artifactV4Routes) deleteArtifact(ctx *ArtifactContext) {
	var req DeleteArtifactRequest

	if ok := r.parseProtbufBody(ctx, &req); !ok {
		return
	}
	_, runID, ok := validateRunIDV4(ctx, req.WorkflowRunBackendId)
	if !ok {
		return
	}
	safeRunPath := safeResolve(r.baseDir, fmt.Sprint(runID))
	safePath := safeResolve(safeRunPath, req.Name)

	_ = os.RemoveAll(safePath)

	respData := DeleteArtifactResponse{
		Ok:         true,
		ArtifactId: artifactNameToID(req.Name),
	}
	r.sendProtbufBody(ctx, &respData)
}
