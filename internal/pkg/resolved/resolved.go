package resolved

// Resolved is configuration that is resolved by looking up in the cluster

type SslCert struct {
	SslClientKey  string
	SslClientCert string
	SslCaCert     string
}

type Resolved struct {
	GcpProjectId     string
	InstanceName     string
	InstanceIp       string
	DbPassword       string
	TargetDbPassword string
	SourceSslCert    SslCert
	TargetSslCert    SslCert
}
