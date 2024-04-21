# Overview
This is an experiment/demo of how to use (htmx)[https://htmx.org/] to build a
dashboard. The ideas is to use it for an embedded device with network
capabilities but no internet access (local area networks). Even though it should
work with multiple clients, it was designed for a single client.

This demo builds an executable with all dependencies embeded in the binary. It
makes use of the http-server in the go standard library to present a htmx-page
with three sections: Status, Config, Log.

## Status
The server updates this section periodically. It can not be modified by the
client. The client polls this section periodically.

## Config
This section gets updated by the client. The config can be edited with a form,
that is rendered when pressing the 'Edit'- Button. It can be Canceled or Saved.
If there are any parsing errors or the values are not in range, then the user is
presented with the error and given the opportunity to adjust or Cancel the
operation.

## Log
This section is polled periodically by the client using 'load delay:xs'. It
passes a sequence number to the server. The server filters out all messages,
that the client received already and sends the rest with the updated sequence
number to the client. After the load delay, the client will then issue another
request with the updated sequence number. The server stores only very few log
messages and they are stored in the dom on the client side by appending to the
table. If the server detects, that there is a gap in the sequence number of the
request and the last stored log message. It will let the client know, that it
dropped x messages. This happens for example when the page is loaded for the
fist time.

# Build and Run
Download `htmx.min.js` and put it in the same directory as `main.go`. If you do
not want to embed the library into the executable, replace the script tag in the
`index.html` as per the [documentation](https://htmx.org/docs/#via-a-cdn-e-g-unpkg-com)
to make use of a content delivery network (and remove the respective code from
`main.go`). This path is not chosen for the demo, because the demo assumes, that
internet access is not available.

Build the binary with `go build` and run the created binary or use `go run main.go`
instead.


