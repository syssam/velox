# Shop Example

A real-world e-commerce example demonstrating Velox with:
- **gqlgen** - GraphQL server
- **shopspring/decimal** - Precise decimal handling for prices
- **PostgreSQL** - Database
- **UUID** - Primary keys for customers and orders

## Project Structure

```
shop/
├── schema/              # Velox schema definitions
│   ├── product.go       # Product entity with decimal prices
│   ├── customer.go      # Customer with UUID ID and addresses
│   ├── order.go         # Order with decimal totals
│   └── order_item.go    # Order line items
├── velox/               # Generated ORM code (don't edit)
├── graph/               # GraphQL code
│   ├── schema.graphql   # Custom GraphQL types
│   ├── ent.graphql      # Auto-generated entity schema
│   ├── resolver.go      # Resolver setup
│   └── model/           # Custom GraphQL models
├── generate.go          # Code generation script
├── gqlgen.yml           # gqlgen configuration
├── main.go              # Server entry point
└── go.mod
```

## Prerequisites

1. **Go 1.23+**
2. **PostgreSQL** running locally or via Docker:
   ```bash
   docker run -d \
     --name shop-postgres \
     -e POSTGRES_USER=postgres \
     -e POSTGRES_PASSWORD=postgres \
     -e POSTGRES_DB=shop \
     -p 5432:5432 \
     postgres:16-alpine
   ```

## Setup

1. **Install dependencies:**
   ```bash
   go mod tidy
   ```

2. **Generate Velox ORM and GraphQL code:**
   ```bash
   go run generate.go
   ```

3. **Generate gqlgen resolvers:**
   ```bash
   go run github.com/99designs/gqlgen generate
   ```

4. **Run the server:**
   ```bash
   go run main.go
   ```

5. **Open GraphQL Playground:**
   http://localhost:8080/

## Example Queries

### Create a Product

```graphql
mutation CreateProduct {
  createProduct(input: {
    name: "Wireless Mouse"
    sku: "MOUSE-001"
    description: "Ergonomic wireless mouse"
    price: "29.99"
    stockQuantity: 100
    status: active
    isFeatured: true
  }) {
    id
    name
    sku
    price
    status
  }
}
```

### Create a Customer

```graphql
mutation CreateCustomer {
  createCustomer(input: {
    email: "john@example.com"
    firstName: "John"
    lastName: "Doe"
    phone: "+1234567890"
    shippingAddress: {
      street: "123 Main St"
      city: "New York"
      state: "NY"
      postalCode: "10001"
      country: "USA"
    }
  }) {
    id
    email
    firstName
    lastName
  }
}
```

### Query Products with Pagination

```graphql
query Products {
  products(first: 10, orderBy: { field: CREATED_AT, direction: DESC }) {
    edges {
      node {
        id
        name
        sku
        price
        stockQuantity
        status
      }
      cursor
    }
    pageInfo {
      hasNextPage
      endCursor
    }
    totalCount
  }
}
```

### Query Products with Filters

```graphql
query ActiveProducts {
  products(
    first: 10
    where: {
      status: active
      isFeatured: true
      stockQuantityGT: 0
    }
  ) {
    edges {
      node {
        id
        name
        price
      }
    }
  }
}
```

### Query Customer with Orders

```graphql
query CustomerOrders {
  customer(id: "uuid-here") {
    id
    email
    firstName
    lastName
    orders(first: 5, orderBy: { field: CREATED_AT, direction: DESC }) {
      edges {
        node {
          id
          orderNumber
          status
          total
          items {
            productName
            quantity
            unitPrice
            lineTotal
          }
        }
      }
    }
  }
}
```

## Schema Features

### Decimal Fields
Uses `shopspring/decimal` for precise monetary calculations:
```go
field.Other("price", decimal.Decimal{}).
    SchemaType(map[string]string{
        "postgres": "DECIMAL(10,2)",
    })
```

### UUID Primary Keys
Customers and Orders use UUID IDs:
```go
field.UUID("id", uuid.UUID{}).
    Default(uuid.New).
    Immutable()
```

### JSON Fields
Addresses stored as JSON:
```go
field.JSON("shipping_address", &Address{}).
    Optional().
    Nillable()
```

### Enum Fields
Status fields with predefined values:
```go
field.Enum("status").
    Values("draft", "active", "discontinued").
    Default("draft")
```

### GraphQL Annotations
Control GraphQL generation per-entity:
```go
graphql.RelayConnection(),           // Enable Relay cursor pagination
graphql.QueryField(),                // Add to Query type
graphql.Mutations(                   // Enable specific mutations
    graphql.MutationCreate(),
    graphql.MutationUpdate(),
),
graphql.Skip(graphql.SkipWhereInput) // Skip WhereInput generation
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/shop?sslmode=disable` | PostgreSQL connection string |
