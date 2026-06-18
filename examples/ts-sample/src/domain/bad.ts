// The planted violation: the domain layer reaches into the db (infrastructure) layer
// through the "@/" path alias, which architecture.json forbids ("domain": []).
// archlint resolves the alias and flags this; tsc alone would happily compile it.
import { findOrder } from '@/db/repo';

export const lookup = findOrder;
