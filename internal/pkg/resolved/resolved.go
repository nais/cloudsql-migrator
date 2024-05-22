package resolved

// Resolved is configuration that is resolved by looking up in the cluster

type SslCert struct {
	SslClientKey  string
	SslClientCert string
	SslCaCert     string
}

type Resolved struct {
	GcpProjectId       string
	DatabaseName       string
	SourceInstanceName string
	TargetInstanceName string
	SourceInstanceIp   string
	TargetInstanceIp   string
	SourceDbPassword   string
	TargetDbPassword   string
	SourceSslCert      SslCert
	TargetSslCert      SslCert
}
