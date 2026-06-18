# The domain layer is the core of the app and imports nothing else inside it
# (architecture.json: "domain": []).
from dataclasses import dataclass


@dataclass
class Order:
    id: int
    amount: float
