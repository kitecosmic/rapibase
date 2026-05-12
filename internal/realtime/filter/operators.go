package filter

import "errors"

// Operator names accepted by the filter language. The set is closed: any
// other operator causes Compile to return errInvalidOperator.
const (
	OpAnd         = "and"
	OpOr          = "or"
	OpNot         = "not"
	OpEq          = "eq"
	OpNeq         = "neq"
	OpLt          = "lt"
	OpLte         = "lte"
	OpGt          = "gt"
	OpGte         = "gte"
	OpIn          = "in"
	OpNotIn       = "nin"
	OpIs          = "is"
	OpLike        = "like"
	OpILike       = "ilike"
	OpContains    = "contains"
	OpContainedBy = "contained_by"
	OpMatch       = "match"
)

// Errors surfaced by the compile path. Wrapped with column / operator
// context by the caller when reporting back through protocol.Error.
var (
	errNotImplemented   = errors.New("filter: compile not yet implemented")
	errInvalidOperator  = errors.New("filter: unknown operator")
	errInvalidNode      = errors.New("filter: invalid node shape")
	errIncompatibleType = errors.New("filter: operator incompatible with column type")
	errUnknownColumn    = errors.New("filter: unknown column")
)
