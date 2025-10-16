Backend Body Templates Suite

This example demonstrates templated backend request bodies using both inline templates
and file-backed templates rendered within the sandbox.

How to run

- The suite is illustrative and does not start a real backend. Point `backendApi.url`
  at your API (or a local echo server) to observe the request payload and headers.
- Enable the template sandbox and (optionally) allow specific environment variables.

Files

- server.yaml — endpoint and rule configuration showing inline and file-based bodies
- templates/requests/payload.json.tmpl — JSON payload rendered for the file-based example

