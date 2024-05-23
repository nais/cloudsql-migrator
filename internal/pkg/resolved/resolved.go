package resolved

import "fmt"

// Resolved is configuration that is resolved by looking up in the cluster

type SslCert struct {
	SslClientKey  string
	SslClientCert string
	SslCaCert     string
}

type Instance struct {
	Name             string
	Ip               string
	AppUsername      string
	AppPassword      string
	PostgresPassword string
	SslCert          SslCert
}

type Resolved struct {
	GcpProjectId string
	DatabaseName string
	Source       Instance
	Target       Instance
}

func (r *Resolved) GcpParentURI() string {
	return fmt.Sprintf("projects/%s/locations/europe-north1", r.GcpProjectId)
}

func (r *Resolved) GcpComponentURI(kind, name string) string {
	return fmt.Sprintf("%s/%s/%s", r.GcpParentURI(), kind, name)
}
