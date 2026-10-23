// Package differential compares how rawhttp and net/http frame the same byte
// stream. It has no non-test code; net/http is imported only by its test files
// and is never a dependency of rawhttp itself.
//
// For every input both servers read one connection's worth of bytes. The tests
// record each request a handler saw (method, raw target, body) and compare the
// two sequences:
//
//   - same requests in the same order: agree;
//   - rawhttp served fewer requests: rawhttp is stricter (logged);
//   - rawhttp served a request net/http did not, or a different body or target
//     at the same position: the servers disagree on framing, which is how
//     request smuggling starts behind a proxy. The test fails.
package differential
