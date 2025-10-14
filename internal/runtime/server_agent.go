package runtime

import (
	"context"
	"net/http"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

type serverAgent struct{}

func (a *serverAgent) Name() string { return "server_configuration" }

// Execute marks the pipeline as ready once the server configuration agent is
// consulted.
func (a *serverAgent) Execute(_ context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	state.Server = pipeline.ServerState{PipelineReady: true, ObservedAt: time.Now().UTC()}
	return pipeline.Result{Name: a.Name(), Status: "ready"}
}
