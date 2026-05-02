package notes

import (
	"net/http"

	"github.com/bokwoon95/nbi4"
)

type Module struct {
	Notebrew  *nbi4.Notebrew
	Namespace string
}

var _ nbi4.Module = (*Module)(nil)

func (m *Module) ID() string { return "github.com/bokwoon95/notebrew/notes" }

func (m *Module) PreferredNamespace() string { return "notes" }

func (m *Module) Initialize(nbrew *nbi4.Notebrew, namespace string) error {
	m.Notebrew = nbrew
	m.Namespace = namespace
	return nil
}

func (m *Module) ServeHTTPContextData(w http.ResponseWriter, r *http.Request, contextData nbi4.ContextData) {
}
