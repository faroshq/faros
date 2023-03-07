package rest

import (
	restclient "k8s.io/client-go/rest"
)

func ContentTypeJSON(r *restclient.Request) *restclient.Request {
	return r.SetHeader("Content-Type", "application/json")
}
