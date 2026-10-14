package notentity

// Report has a method named Schema that is not velox's.
type Report struct{ ID int64 }

func (r *Report) Schema() string { return "report" }
