package workflowcheck

import (
	"fmt"
	"net/http/httptest"
	"os"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/httpapi"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

func Run(name string) error {
	dataDir, err := os.MkdirTemp("", "cellar-run-check-*")
	if err != nil {
		return fmt.Errorf("create check directory: %w", err)
	}
	defer os.RemoveAll(dataDir)

	if name == "formula-run-comparison" {
		if err := formulaRunComparison(dataDir); err != nil {
			return err
		}
		fmt.Printf("workflow check passed: %s\n", name)
		return nil
	}

	server, err := startCheckServer(dataDir)
	if err != nil {
		return err
	}
	defer server.close()
	api := newClient(server.url)

	switch name {
	case "formula-approval":
		if err := formulaApproval(api); err != nil {
			return err
		}
	case "vessel-run-lifecycle":
		if err := vesselRunLifecycle(api); err != nil {
			return err
		}
	case "observations-completion":
		if err := observationsCompletion(api); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown workflow check %q", name)
	}
	fmt.Printf("workflow check passed: %s\n", name)
	return nil
}

type checkServer struct {
	server *httptest.Server
	url    string
}

func startCheckServer(dataDir string) (*checkServer, error) {
	store, err := storage.Open(dataDir)
	if err != nil {
		return nil, err
	}
	app := service.New(store, clock.System{})
	server := httptest.NewServer(httpapi.NewHandler(app))
	return &checkServer{server: server, url: server.URL}, nil
}

func (c *checkServer) close() {
	c.server.Close()
}
