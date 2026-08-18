# Substrate API Tool (`apitool`)

`apitool` works with the `Control` service in `pkg/proto/ateapipb/ateapi.proto`. `generate` builds a self-contained HTML reference page for it; `validate` checks the API against a set of shape rules (e.g. every resource has a metadata field) and reports any violations.

## Usage

```bash
cd tools/apitool
go run . generate   # writes bin/api-reference.html
go run . validate   # exits nonzero on any rule violation
```
