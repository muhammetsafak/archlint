// The db layer may import the domain layer (rules: "db": ["domain"]).
import { Order } from '../domain/order';

export function findOrder(id: number): Order | null {
  return id > 0 ? { id, amount: 0 } : null;
}
