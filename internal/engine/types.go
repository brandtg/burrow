// Package engine holds the run pipeline's shared contract: the resolved
// environment and the Target/Action/Category/Guard types.
package engine

import (
	"context"
	"errors"
	"os/user"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

// Recovery is the declared recovery contract for a mutation (invariant I2).
type Recovery int

const (
	// RecoveryTrash moves the item to the freedesktop Trash (recoverable).
	RecoveryTrash Recovery = iota + 1
	// RecoveryOwner invokes the owner command (permanent but re-downloadable).
	RecoveryOwner
)

func (r Recovery) String() string {
	switch r {
	case RecoveryTrash:
		return "trash"
	case RecoveryOwner:
		return "owner"
	default:
		return "invalid"
	}
}

// Valid reports whether r is a known contract.
func (r Recovery) Valid() bool { return r == RecoveryTrash || r == RecoveryOwner }

// Risk drives default selection in the preview.
type Risk int

const (
	// RiskLow is safe to pre-select.
	RiskLow Risk = iota
	// RiskMed is shown but not pre-selected by default.
	RiskMed
	// RiskHigh is never auto-selected.
	RiskHigh
)

func (r Risk) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMed:
		return "med"
	case RiskHigh:
		return "high"
	default:
		return "unknown"
	}
}

// Regen classifies how expensive a target is to regenerate. It is independent
// of Risk, which describes the blast radius of the deletion itself. Only Free
// and Cheap targets are ever pre-selected or bulk-selected; Expensive and Data
// targets are shown for the user to choose explicitly. The zero value is Free,
// so targets that predate this field stay selectable.
type Regen int

const (
	// RegenFree is regenerated automatically at no meaningful cost.
	RegenFree Regen = iota
	// RegenCheap is regenerated quickly, at most a small download.
	RegenCheap
	// RegenExpensive needs a large download, network access, or a long rebuild.
	RegenExpensive
	// RegenData is not meaningfully regenerable and should be treated as data.
	RegenData
)

func (r Regen) String() string {
	switch r {
	case RegenFree:
		return "free"
	case RegenCheap:
		return "cheap"
	case RegenExpensive:
		return "expensive"
	case RegenData:
		return "data"
	default:
		return "unknown"
	}
}

// Result is the outcome of executing an Action.
type Result struct {
	Status   string
	Bytes    int64
	Reason   string
	Argv     []string
	TimedOut bool
}

// Status values for a planned item and an action result.
const (
	StatusPending = "pending"
	StatusOK      = "ok"
	StatusSkipped = "skipped"
	StatusFailed  = "failed"
	StatusDryRun  = "dry-run"
)

// ArgvPlanner is implemented by owner-command actions so the review screen can
// list the exact argv that will run. It is an optional interface; the Action
// contract itself is unchanged.
type ArgvPlanner interface {
	PlannedArgv() []string
}

// Action performs a mutation. Implementations must be idempotent and must
// re-validate identity at the moment of execution.
type Action interface {
	Describe() string
	// Recovery declares the contract; it must be valid.
	Recovery() Recovery
	// Run executes; dryRun must compute the same plan without mutating.
	Run(ctx context.Context, dryRun bool) (Result, error)
}

// Guard fails closed: it returns an error (guard-stop, code 75) when it cannot
// prove the action is safe right now.
type Guard interface {
	Check(ctx context.Context, env *Env) error
}

// Target is one concrete thing the tool might act on.
//
// Group and GroupTitle are presentation-only: they let a category that produces
// many similar targets (for example one per project's __pycache__) collapse into
// meaningful sub-rows. Execution, guards, and identity snapshots stay per-Target,
// so grouping never changes what a mutation does (I3/I4).
//
// Regen and Gated affect selection only. Regen bounds how expensive the item is
// to rebuild; Gated marks an item a policy advises keeping (for example a cache
// touched within the idle window). Neither changes how an item executes.
type Target struct {
	ID         string
	Category   string
	Display    string
	Path       string
	SizeBytes  int64
	SizeKnown  bool
	Action     Action
	Recovery   Recovery
	NeedsRoot  bool
	Guard      Guard
	Evidence   []string
	Risk       Risk
	Regen      Regen
	Gated      bool
	DefaultOn  bool
	Group      string
	GroupTitle string
}

// AutoSelectable reports whether the target may be pre-selected or selected in
// bulk. Expensive/data items and policy-gated items are excluded; the user can
// still toggle them individually.
func (t Target) AutoSelectable() bool {
	return !t.Gated && t.Risk != RiskHigh && t.Regen <= RegenCheap
}

// Validate enforces invariant I2: every mutation declares a valid recovery
// contract and has an action.
func (t Target) Validate() error {
	if t.ID == "" {
		return errors.New("engine: target has no id")
	}
	if !t.Recovery.Valid() {
		return errors.New("engine: target must declare a recovery contract (trash|owner)")
	}
	if t.Action == nil {
		return errors.New("engine: target has no action")
	}
	if t.Action.Recovery() != t.Recovery {
		return errors.New("engine: action recovery does not match target recovery")
	}
	if t.Recovery == RecoveryTrash && t.Path == "" {
		return errors.New("engine: trash-contract target must have a path")
	}
	return nil
}

// Category enumerates targets. Discovery must be side-effect-free.
type Category interface {
	ID() string
	Title() string
	Discover(ctx context.Context, env *Env) ([]Target, error)
	PreviewNote() string
}

// Env carries the resolved user/root context and external command runners.
type Env struct {
	User       *user.User
	Home       string
	XDGCache   string
	XDGConfig  string
	XDGData    string
	XDGState   string
	XDGRuntime string

	Run       *owner.Runner
	Whitelist *whitelist.Set
	Cfg       *config.Config
	Distro    distro.Info
	Adapter   distro.Adapter
	Paths     config.Paths
	DryRun    bool
}
