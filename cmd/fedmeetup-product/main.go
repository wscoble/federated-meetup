// SPDX-License-Identifier: AGPL-3.0
//
// Product daemon — mounts the ProductService on a standalone HTTP server.
// For smoke testing the product RPC surface without the protocol layer.

package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/sscoble/federated-meetup/internal/product"
	"github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1/federatedmeetupproductv1connect"

	"connectrpc.com/connect"
)

func main() {
	store := product.NewStore()
	svc := product.NewService(store)

	mux := http.NewServeMux()
	path, handler := federatedmeetupproductv1connect.NewProductServiceHandler(
		svc,
		connect.WithCompressMinBytes(0),
	)
	mux.Handle(path, handler)

	addr := ":18081"
	fmt.Printf("ProductService daemon listening on %s\n", addr)
	fmt.Printf("Endpoint: http://127.0.0.1%s%s\n", addr, path)
	log.Fatal(http.ListenAndServe(addr, mux))
}