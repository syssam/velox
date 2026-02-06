package model

import "github.com/google/uuid"

// AddressInput represents a customer address input.
type AddressInput struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postalCode"`
	Country    string `json:"country"`
}

// OrderItemInput represents an order item for creating orders.
type OrderItemInput struct {
	ProductID int64 `json:"productId"`
	Quantity  int   `json:"quantity"`
}

// CreateOrderWithItemsInput represents input for creating an order with items.
type CreateOrderWithItemsInput struct {
	CustomerID      uuid.UUID        `json:"customerId"`
	ShippingAddress AddressInput     `json:"shippingAddress"`
	Notes           *string          `json:"notes,omitempty"`
	Items           []OrderItemInput `json:"items"`
}
