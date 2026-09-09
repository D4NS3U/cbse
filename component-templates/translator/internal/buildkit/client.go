// client.go is the real BuildKit client adapter. It wraps the official
// moby/buildkit Go client behind the testable buildkit.ListWorkersFunc and
// buildkit.SolveFunc seams. The orchestrator and main wire these closures; the
// unit tests inject fakes and never touch a real buildkitd.
package buildkit

import (
	"context"
	"fmt"
	"os"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/registryauth"
	dockertypes "github.com/docker/cli/cli/config/types"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/tonistiigi/fsutil"
)

// BuildkitAddress is the canonical sidecar socket address.
const BuildkitAddress = "unix:///run/buildkit/buildkitd.sock"

// NewClient connects to the BuildKit sidecar at the canonical socket.
func NewClient(ctx context.Context) (*bkclient.Client, error) {
	return bkclient.New(ctx, BuildkitAddress)
}

// ListWorkersFuncFor returns a ListWorkersFunc backed by the given client.
func ListWorkersFuncFor(c *bkclient.Client) ListWorkersFunc {
	return func(ctx context.Context) (int, error) {
		workers, err := c.ListWorkers(ctx)
		if err != nil {
			return 0, err
		}
		return len(workers), nil
	}
}

// SolveFuncFor returns a SolveFunc backed by the given client, authenticating
// registry pushes through the mounted Docker configuration resolved by auth.
func SolveFuncFor(c *bkclient.Client, auth *registryauth.Config) SolveFunc {
	return func(ctx context.Context, opt SolveOptions, status chan<- Status) (string, error) {
		fs, err := fsutil.NewFS(opt.BuildDir)
		if err != nil {
			return "", fmt.Errorf("buildkit: build context: %w", err)
		}
		attrs := map[string]string{
			"name": opt.Tag,
			"push": "true",
		}
		for k, v := range opt.Annotations {
			attrs["annotation-manifest."+k] = v
		}
		bkStatus := make(chan *bkclient.SolveStatus)
		go func() {
			for s := range bkStatus {
				if status != nil {
					for _, v := range s.Vertexes {
						status <- Status{Vertex: v.Digest.String()}
					}
				}
			}
		}()
		resp, err := c.Solve(ctx, nil, bkclient.SolveOpt{
			Exports: []bkclient.ExportEntry{{
				Type:  bkclient.ExporterImage,
				Attrs: attrs,
			}},
			LocalMounts: map[string]fsutil.FS{"context": fs},
			Frontend:    "dockerfile.v0",
			FrontendAttrs: map[string]string{
				"filename": "Dockerfile",
			},
			Session: []session.Attachable{authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
				AuthConfigProvider: func(ctx context.Context, host string, _ []string, _ authprovider.ExpireCachedAuthCheck) (dockertypes.AuthConfig, error) {
					return resolveAuth(auth, host)
				},
			})},
		}, bkStatus)
		if err != nil {
			return "", fmt.Errorf("buildkit: solve: %w", err)
		}
		digest := resp.ExporterResponse["containerimage.digest"]
		if digest == "" {
			return "", fmt.Errorf("buildkit: solve returned no digest")
		}
		return digest, nil
	}
}

// resolveAuth returns the docker-cli AuthConfig for host from the mounted
// Docker configuration. A missing credential is anonymous (zero value, no
// error); the registry then rejects the push if it requires auth.
func resolveAuth(auth *registryauth.Config, host string) (dockertypes.AuthConfig, error) {
	if auth == nil {
		return dockertypes.AuthConfig{}, nil
	}
	user, pass, err := auth.ResolveBasicAuth(host)
	if err != nil {
		// No entry for this host: attempt anonymous.
		return dockertypes.AuthConfig{}, nil
	}
	return dockertypes.AuthConfig{Username: user, Password: pass}, nil
}

// ensure os is used (for potential future fsutil options).
var _ = os.Stat
