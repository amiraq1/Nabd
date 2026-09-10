package provider

// Encoder is implemented by providers that can render a request exactly as
// they would put it on the wire, without sending it.
//
// It exists so cost can be measured from the real encoder instead of
// estimated: the bytes returned here are the bytes attempt() would POST. A
// second implementation that drifted from attempt() would be worse than no
// introspection at all, so the method is a thin wrapper over the same encode
// path used to build the request (see the Encode methods below).
type Encoder interface {
	Encode(Request) ([]byte, error)
}

var (
	_ Encoder = (*Anthropic)(nil)
	_ Encoder = (*OpenAICompat)(nil)
)

// Encode renders the request in the Anthropic wire format without sending it.
func (a *Anthropic) Encode(req Request) ([]byte, error) { return a.encode(req) }

// Encode renders the request in the OpenAI-compatible wire format without
// sending it.
func (o *OpenAICompat) Encode(req Request) ([]byte, error) { return o.encode(req) }
