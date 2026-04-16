package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFederationKey(t *testing.T) {
	a := FederationKey("id")
	assert.Len(t, a.Directives, 1)
	assert.Equal(t, "key", a.Directives[0].Name)
	assert.Equal(t, "id", a.Directives[0].Args["fields"])
}

func TestFederationKey_CompositeFields(t *testing.T) {
	a := FederationKey("id organization { id }")
	assert.Len(t, a.Directives, 1)
	assert.Equal(t, "key", a.Directives[0].Name)
	assert.Equal(t, "id organization { id }", a.Directives[0].Args["fields"])
}

func TestFederationKeyResolvable(t *testing.T) {
	a := FederationKeyResolvable("id", false)
	assert.Len(t, a.Directives, 1)
	d := a.Directives[0]
	assert.Equal(t, "key", d.Name)
	assert.Equal(t, "id", d.Args["fields"])
	assert.Equal(t, false, d.Args["resolvable"])
}

func TestFederationExternal(t *testing.T) {
	a := FederationExternal()
	assert.Len(t, a.Directives, 1)
	assert.Equal(t, "external", a.Directives[0].Name)
	assert.Nil(t, a.Directives[0].Args)
}

func TestFederationRequires(t *testing.T) {
	a := FederationRequires("price currency")
	assert.Len(t, a.Directives, 1)
	d := a.Directives[0]
	assert.Equal(t, "requires", d.Name)
	assert.Equal(t, "price currency", d.Args["fields"])
}

func TestFederationProvides(t *testing.T) {
	a := FederationProvides("name")
	assert.Len(t, a.Directives, 1)
	d := a.Directives[0]
	assert.Equal(t, "provides", d.Name)
	assert.Equal(t, "name", d.Args["fields"])
}

func TestFederationShareable(t *testing.T) {
	a := FederationShareable()
	assert.Len(t, a.Directives, 1)
	assert.Equal(t, "shareable", a.Directives[0].Name)
	assert.Nil(t, a.Directives[0].Args)
}

func TestFederationInaccessible(t *testing.T) {
	a := FederationInaccessible()
	assert.Len(t, a.Directives, 1)
	assert.Equal(t, "inaccessible", a.Directives[0].Name)
	assert.Nil(t, a.Directives[0].Args)
}

func TestFederationOverride(t *testing.T) {
	a := FederationOverride("products")
	assert.Len(t, a.Directives, 1)
	d := a.Directives[0]
	assert.Equal(t, "override", d.Name)
	assert.Equal(t, "products", d.Args["from"])
}

func TestFederationCombined(t *testing.T) {
	a := MergeAnnotations(
		FederationKey("id"),
		FederationShareable(),
	)
	assert.Len(t, a.Directives, 2)
	assert.Equal(t, "key", a.Directives[0].Name)
	assert.Equal(t, "shareable", a.Directives[1].Name)
}
