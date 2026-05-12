// Structured filter builder. Produces the JSON tree the server's
// filter package compiles into a predicate. Designed to be chainable
// and to keep the call site readable when filters get complex.
//
// Example:
//   filter((q) => q.and(
//     q.eq('room_id', 42),
//     q.in('status', ['open', 'pending']),
//     q.or(
//       q.is('archived', false),
//       q.gte('created_at', cutoff),
//     ),
//   ))

export type FilterNode =
  | LeafNode
  | CompositeNode

export interface LeafNode {
  column: string
  op:
    | 'eq' | 'neq'
    | 'lt' | 'lte' | 'gt' | 'gte'
    | 'in' | 'nin'
    | 'is'
    | 'like' | 'ilike'
    | 'contains' | 'contained_by'
    | 'match'
  value: unknown
}

export interface CompositeNode {
  op: 'and' | 'or' | 'not'
  conditions: FilterNode[]
}

/**
 * FilterBuilder is the fluent factory you receive in a filter
 * callback. Each method returns a FilterNode that can be combined
 * with and/or/not.
 */
export interface FilterBuilder {
  eq(column: string, value: unknown): LeafNode
  neq(column: string, value: unknown): LeafNode
  lt(column: string, value: unknown): LeafNode
  lte(column: string, value: unknown): LeafNode
  gt(column: string, value: unknown): LeafNode
  gte(column: string, value: unknown): LeafNode
  in(column: string, values: unknown[]): LeafNode
  nin(column: string, values: unknown[]): LeafNode
  is(column: string, value: null | true | false): LeafNode
  like(column: string, pattern: string): LeafNode
  ilike(column: string, pattern: string): LeafNode
  contains(column: string, value: unknown): LeafNode
  containedBy(column: string, value: unknown): LeafNode
  match(column: string, needle: string): LeafNode

  and(...conditions: FilterNode[]): CompositeNode
  or(...conditions: FilterNode[]): CompositeNode
  not(condition: FilterNode): CompositeNode
}

const builder: FilterBuilder = {
  eq: (column, value) => ({ column, op: 'eq', value }),
  neq: (column, value) => ({ column, op: 'neq', value }),
  lt: (column, value) => ({ column, op: 'lt', value }),
  lte: (column, value) => ({ column, op: 'lte', value }),
  gt: (column, value) => ({ column, op: 'gt', value }),
  gte: (column, value) => ({ column, op: 'gte', value }),
  in: (column, values) => ({ column, op: 'in', value: values }),
  nin: (column, values) => ({ column, op: 'nin', value: values }),
  is: (column, value) => ({ column, op: 'is', value }),
  like: (column, pattern) => ({ column, op: 'like', value: pattern }),
  ilike: (column, pattern) => ({ column, op: 'ilike', value: pattern }),
  contains: (column, value) => ({ column, op: 'contains', value }),
  containedBy: (column, value) => ({ column, op: 'contained_by', value }),
  match: (column, needle) => ({ column, op: 'match', value: needle }),

  and: (...conditions) => ({ op: 'and', conditions }),
  or: (...conditions) => ({ op: 'or', conditions }),
  not: (condition) => ({ op: 'not', conditions: [condition] }),
}

/**
 * Build a filter tree using the provided callback. The result is the
 * JSON-serialisable structure the server expects in
 * PostgresChangesConfig.filter.
 */
export function filter(
  build: (q: FilterBuilder) => FilterNode,
): FilterNode {
  return build(builder)
}

/** Re-export the builder instance for callers that prefer direct use. */
export const q: FilterBuilder = builder
