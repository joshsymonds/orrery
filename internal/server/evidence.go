package server

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// evidenceServer answers orrery.v1.Evidence by reading artifacts from client. A nil client answers unavailable.
type evidenceServer struct {
	client  EvidenceClient
	timeout time.Duration
}

// newEvidenceHandler returns the path and handler that serve Evidence over client, reading at most maxControlBody bytes
// of each request. A zero timeout is defaultControlTimeout.
func newEvidenceHandler(client EvidenceClient, timeout time.Duration) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewEvidenceHandler(
		evidenceServer{client: client, timeout: timeout},
		connect.WithReadMaxBytes(maxControlBody),
	)
}

// evidenceUnavailable is the fixed message for an Artifact call when serve has no evidence client.
const evidenceUnavailable = "evidence service unavailable"

// mediaTypeText is the media type of the run's log and of the text files, and mediaTypeOctets that of an artifact whose
// extension no media type is known for.
const (
	mediaTypeText   = "text/plain; charset=utf-8"
	mediaTypeOctets = "application/octet-stream"
)

// Artifact answers the bytes of one artifact of a run, or of the run's log when the name is empty, with the media type
// artifactMediaType gives the name. The call runs under the timeout and is not retried. A nil client answers
// unavailable, and an orreryd failure answers as translateError answers a failed forward.
func (e evidenceServer) Artifact(
	ctx context.Context,
	req *connect.Request[orreryv1.EvidenceArtifactRequest],
) (*connect.Response[orreryv1.EvidenceArtifactResponse], error) {
	if e.client == nil {
		return nil, listError(connect.CodeUnavailable, evidenceUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	name := req.Msg.GetName()
	resp, err := e.client.GetEvidenceArtifact(ctx, connect.NewRequest(&enginev1.GetEvidenceArtifactRequest{
		Run:  req.Msg.GetRun(),
		Name: name,
	}))
	if err != nil {
		return nil, translateError(wrapEngine("evidence artifact", err))
	}
	return connect.NewResponse(&orreryv1.EvidenceArtifactResponse{
		Data:      resp.Msg.GetData(),
		MediaType: artifactMediaType(name),
	}), nil
}

// artifactMediaType returns the media type of the artifact at name by its extension, compared without case: image/png,
// image/jpeg for .jpg and .jpeg, image/gif, image/webp, text/plain for .txt and .log, and application/json for .json.
// Any other extension is application/octet-stream. The empty name is the run's log, which is text/plain.
func artifactMediaType(name string) string {
	if name == "" {
		return mediaTypeText
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt", ".log":
		return mediaTypeText
	case ".json":
		return "application/json"
	default:
		return mediaTypeOctets
	}
}
