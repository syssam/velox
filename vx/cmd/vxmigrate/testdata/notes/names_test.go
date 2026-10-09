package notes

// post is a name of the package's own, declared in a test: the file that
// comes to need the package post must not import it under this name.
var post = "a name of the tests"

var _ = post
