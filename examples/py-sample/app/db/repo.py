# The db layer may import the domain layer (rules: "db": ["domain"]).
from ..domain.order import Order


def find_order(order_id: int) -> Order | None:
    return Order(id=order_id, amount=0.0) if order_id > 0 else None
