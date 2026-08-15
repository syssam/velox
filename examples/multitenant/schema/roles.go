package schema

// RoleSystem marks a viewer that runs internal work — background jobs,
// reconciliation, admin tooling — and is therefore exempt from the tenant
// write filter. It is deliberately a role on the viewer rather than a
// context flag: a flag is inherited invisibly down a call stack, a role has
// to be constructed.
const RoleSystem = "system"
