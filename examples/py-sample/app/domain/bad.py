# The planted violation: the domain layer reaches up into the db (infrastructure) layer
# with a relative import, which architecture.json forbids ("domain": []).
from ..db.repo import find_order

lookup = find_order
