# Contributing to llmgateway

Thanks for your interest in contributing! The easiest way to contribute is adding a new provider adapter.

---

## Getting Started

```bash
git clone https://github.com/wzhongyou/llmgateway.git
cd llmgateway

# Run integration tests (requires a provider key)
cp testconfig.toml.example testconfig.toml
# edit: deepseek_key = "sk-xxx"
go test ./sdk/ ./gateway/ -v -count=1
```

---

## Adding a Provider

This is the most impactful contribution. See the full guide: [docs/adapter-template.md](docs/adapter-template.md).

Quick flow:

1. Create `core/providers/<name>/<name>.go`
2. Implement the `Provider` interface
3. Register via `init()` → `core.RegisterProvider("name", factory)`
4. Run tests
5. Submit a PR

---

## Code Style

- Standard Go conventions (`gofmt`, `go vet`)
- Package names are lowercase, single word
- Error messages include the provider/package name for traceability

---

## PR Process

1. Fork the repo
2. Create a feature branch
3. Add your changes with tests
4. Run `go build ./... && go vet ./...`
5. Submit a PR

---

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
