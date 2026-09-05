// Package demo is a small, self-contained, entirely fictional example used
// by singlecaller's README/blog post to demonstrate the tool's behavior
// without referencing any real codebase. It lives under testdata/, which Go
// tooling (go build, go vet, "./..." expansion) skips automatically, so it
// never shows up in a real lint run of this module.
package demo

// The four *Row types below stand in for what a code generator (sqlc, ent,
// sqlboiler, ...) typically produces: one distinct struct per query, even
// when the queries return the same logical columns.
type ListOrdersRow struct {
	ID     string
	Status string
}
type GetOrderRow struct {
	ID     string
	Status string
}
type CreateOrderRow struct {
	ID     string
	Status string
}
type UpdateOrderRow struct {
	ID     string
	Status string
}

type Order struct {
	ID     string
	Status string
}

// orderFromFields is called four times, once per distinct row type above.
// This is the shape a shared builder earns its keep: one conversion,
// several genuinely different callers.
func orderFromFields(id, status string) *Order {
	return &Order{ID: id, Status: status}
}

func ListOrders(rows []ListOrdersRow) []*Order {
	out := make([]*Order, len(rows))
	for i, r := range rows {
		out[i] = orderFromFields(r.ID, r.Status)
	}
	return out
}

func GetOrder(row GetOrderRow) *Order       { return orderFromFields(row.ID, row.Status) }
func CreateOrder(row CreateOrderRow) *Order { return orderFromFields(row.ID, row.Status) }
func UpdateOrder(row UpdateOrderRow) *Order { return orderFromFields(row.ID, row.Status) }

// CustomerRow happens to be the only query shape customers ever need.
type CustomerRow struct {
	ID    string
	Email string
}

type Customer struct {
	ID    string
	Email string
}

// customerFromFields was copied from orderFromFields's pattern without
// checking whether the premise — several distinct row shapes to reconcile —
// actually held. It doesn't: there's only ever one CustomerRow, and
// therefore only ever one caller. This is the exact shape singlecaller
// looks for.
func customerFromFields(id, email string) *Customer {
	return &Customer{ID: id, Email: email}
}

func GetCustomer(row CustomerRow) *Customer {
	return customerFromFields(row.ID, row.Email)
}
