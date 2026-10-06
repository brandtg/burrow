// Package catalog is the cleanup-catalog registry. The catalog is data plus a
// small Discover per category; later plans add categories without reshaping
// this framework (plan 03 risk note).
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
