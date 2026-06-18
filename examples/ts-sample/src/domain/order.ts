// The domain layer is the core of the app and imports nothing else inside it
// (architecture.json: "domain": []).
export interface Order {
  id: number;
  amount: number;
}
