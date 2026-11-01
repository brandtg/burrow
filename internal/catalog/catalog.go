// Package catalog is the cleanup-catalog registry. The catalog is data plus a
// small Discover per category; later additions must not reshape this
// framework.
package catalog

import "github.com/brandtg/burrow/internal/engine"

// Registry holds the registered categories in display order.
type Registry struct {
	categories []engine.Category
}

// NewRegistry returns a registry seeded with cats.
func NewRegistry(cats ...engine.Category) *Registry {
	r := &Registry{}
	for _, c := range cats {
		r.Register(c)
	}
	return r
}

// Register appends a category.
func (r *Registry) Register(c engine.Category) {
	if c == nil {
		return
	}
	r.categories = append(r.categories, c)
}

// Categories returns the registered categories.
func (r *Registry) Categories() []engine.Category {
	return append([]engine.Category(nil), r.categories...)
}

// ByID returns the category with the given id.
func (r *Registry) ByID(id string) (engine.Category, bool) {
	for _, c := range r.categories {
		if c.ID() == id {
			return c, true
		}
	}
	return nil, false
}

// Reconciler is an optional category hook run after Build and before preview.
// It lets a category drop items that became redundant given the user's
// selection (for example a superset prune superseding its parts) without the
// engine knowing any category's internals.
type Reconciler interface {
	Reconcile(*engine.Plan)
}

// Warner is an optional category hook exposing non-fatal discovery notes (for
// example a configured root that does not exist) for the plan's warning list.
// Discovery never aborts on a bad input; the warning is the only signal.
type Warner interface {
	Warnings() []string
}
