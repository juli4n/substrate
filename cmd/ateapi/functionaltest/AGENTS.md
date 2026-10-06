# ate-api functional tests

`specs.yaml` is the source of truth for the tests in this directory. It lists
every test's spec: its name, the resource it belongs to, and a natural-language
description of what it checks. The Go tests implement those descriptions. When
asked to reconcile the tests, make the code match the specs.

Never edit `specs.yaml`. It belongs to its author; agents only change the
test code. When the specs themselves have a problem, such as a duplicate or
malformed name, report it and leave the fix to the author.

## Spec schema

```yaml
tests:
  - name: DeleteAtespace_WithPreconditions
    resource: atespaces
    description: |
      What the test does and asserts, in plain language.
```

- `name` is the name of the Go test and must be unique within this file.
- `resource` is the plural, lowercase name of the resource the test asserts on
  (`atespaces`, `actors`, `workers`, ...).
- `description` is the specification of the test case.
- No other fields are allowed.
- A description states how ate-api currently behaves. When that differs from
  how it should behave, the author describes today's behavior and adds a
  `# TODO:` comment above the entry saying what the server should change to.
  The test follows the description, not the TODO.
- An outcome ate-api reaches in the background, after the RPC returns, is
  described with "eventually". Every other outcome holds when the RPC returns.
- Entries are grouped by resource, each group under a comment header naming
  the resource and its file, with the groups in alphabetical order by
  resource. Add an entry under its resource's header, and start a new header,
  at its alphabetical place, for a new resource.

## Mapping

The mapping is mechanical, with no interpretation:

- Test `name` → Go function `Test<name>`.
- Test `resource` → file `<resource>_test.go`. Every test of a resource lives
  in that one file.
- Spec order → file order. A file declares its tests in the order
  `specs.yaml` lists them, after any helpers the tests share.

## Reconcile procedure

1. Compare the specs with the code:

   ```bash
   go test ./cmd/ateapi/functionaltest/specs/
   ```

   It runs in about a second and fails on:

   - an invalid spec: an unknown field, a duplicate name, a name not
     starting with an uppercase letter, or no resource or description;
   - a test `specs.yaml` lists that does not exist, or sits in the wrong file;
   - a test the code has that `specs.yaml` does not list;
   - a file whose tests are not in spec order.

   It reports the tests of each file as a diff: `-` for what `specs.yaml`
   lists, `+` for what the code declares. `make test` runs the same check, so
   CI rejects a mismatch.

2. Implement each missing test in its resource's file, at its spec's
   position, following [Writing tests](#writing-tests). Move a test that sits
   in the wrong file or out of order.

3. Report tests the code has but `specs.yaml` does not list. Do not delete
   them: the specs may be missing one.

4. Review every test against its description and report drift: an assertion
   the description asks for that the test lacks, one that contradicts it, or a
   different expected value. A test that fails because ate-api behaves
   differently from its description is a discrepancy to report, not a test to
   change: the author either corrects the description or records a `# TODO:`
   for the server. For each drifted test, break the expected
   outcome on purpose and confirm the test fails; a test that still passes
   does not check its description. Report drift rather than rewriting the
   test, unless asked to fix it.

5. Finish with the [acceptance checks](#acceptance).

## Finding gaps

When asked to find gaps, measure which server code the tests reach and report
what is missing. Do not change tests or `specs.yaml` while doing so.

1. Run the suite with coverage of ate-api's server packages. ate-api runs
   inside the test process, so its code counts:

   ```bash
   go test -coverpkg=./cmd/ateapi/internal/controlapi,./cmd/ateapi/internal/workerservice,./cmd/ateapi/internal/server \
     -coverprofile=cover.out ./cmd/ateapi/functionaltest/
   go tool cover -func=cover.out
   ```

2. Classify every uncovered RPC handler or branch:

   - **Missing test:** an RPC, or a branch a client can reach through the
     public API and the harness. Propose a spec.
   - **Harness gap:** production code the harness skips, such as a fake
     plugged in below the code that would normally find it. Propose a harness
     change; it needs careful review.
   - **Not reachable black-box:** a branch only a store failure or a crash
     between two writes can reach. Note that it belongs in the in-package
     tests.

3. Report the proposed entries as plain-language descriptions, using the
   naming in [Mapping](#mapping), for the specs' author to add.

Coverage measures what runs, not what is checked. Never propose an entry only
to cover lines; every entry describes behavior a client can observe.

## Writing tests

The tests are black-box: they drive ate-api only through its public gRPC API
and the harness in `harness/`, never through the store, `RPCService`, or any
other ate-api internal.

- Start each test with `t.Parallel()` and its own `h := harness.New(t)`. The
  harness starts an empty ate-api with its own database and fakes; create the
  atespaces, worker pools, workers and templates the test needs.
- Write one test per case. Do not turn cases with different setup or recovery
  into a table of functions.
- Assert on whole resources: `cmp.Diff(want, got, protocmp.Transform(), ...)`.
  Ignore only fields ate-api assigns, with the options in `cmp_test.go`.
  - For a newly created resource, write `want` as a literal proto.
  - For an operation on an existing resource, read it right before the
    operation, `proto.Clone` it, and change only the fields the operation
    should change.
  - An operation that is refused must leave the resource exactly as it was:
    compare with nothing ignored.
- Assert on the fakes the same way: compare whole `Sandboxes()`,
  `Volumes.List()` or `ObjectStore` contents, not single entries.
- Check an RPC's error by its gRPC code: `status.Code(err) != codes.NotFound`.
  Assert on the error message only when the description explicitly asks for
  something in it, and then only that the message contains it:
  `strings.Contains(status.Convert(err).Message(), "actor.metadata.name")`
  for a description saying the error names the invalid field. Never compare
  the whole message.
- Inject failures through the fakes' `On(op)` faults (`FailNext`,
  `ApplyThenFail`, `Block`, ...), with the error presets in
  `harness/fault.go`.
- Assert an RPC's outcome as soon as it returns: ate-api finishes an RPC's
  work before replying. Wait with `h.Eventually`, polling public RPCs, only
  for an outcome the description says happens eventually. If an assertion
  fails because the outcome arrives later, report it rather than adding a
  wait: the author decides whether the description should say "eventually".
- ate-api's scheduler and atelet lookup trail what a test sets up. Use
  `h.ResumeActor`, which retries while ate-api catches up, unless the test
  asserts that very refusal; then call `h.Control.ResumeActor` once.

## Authentication and authorization

ate-api runs with authentication and authorization enforced. 
Control clients authenticate with a client certificate carrying
a SPIFFE ID, as in-cluster components do; ate-api accepts no bearer tokens.

- `h.Control` authenticates as a bootstrap owner, a global owner whatever the
  stored access policies say. Use it to set up a test, and for every call
  that is not about permissions.
- `h.ClientAs(name)` returns a Control client authenticating as
  `harness.ClientID(name)`. Access policies name it as the member
  `harness.Member(name)`. It holds no permissions until a policy grants them.
- To test a permission, grant a role to `harness.Member(name)` with an access
  policy created through `h.Control`, call the RPC through `h.ClientAs(name)`,
  and check the gRPC code: `OK`, or `PermissionDenied`.
- An `<Method>_Authorization` test checks one method's permission. Its
  description calls `h.Control` the admin and the `h.ClientAs` client the
  principal. Only the principal calls the method under test. The admin makes
  every grant, replacing the principal's single binding before each step, and
  makes every read that checks a refused call changed nothing.
- Only RPCs with a rule in `cmd/ateapi/internal/authz/registry.go` check
  permissions. Any authenticated principal may call the others, so a
  permission test only makes sense for an RPC with a rule.
- Atelets call WorkerService with their pod identity, through
  `Node.WorkerService()`. Access policies do not apply to those calls.

## Acceptance

A reconcile is done when all of these hold:

- The spec check in step 1 passes, or every test it reports as unlisted
  has been reported.
- `go vet ./...` is clean and `gofmt -l .` prints nothing.
- `go test -race -count=3 ./...` passes from this directory.
- Every new test was seen failing once: break one of its expected values, run
  it, and restore the value.

Running the tests needs Docker, for PostgreSQL, and the envtest binaries,
which `hack/run-tool.sh setup-envtest` downloads on first use.
