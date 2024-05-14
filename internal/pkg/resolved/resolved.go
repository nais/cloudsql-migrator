package resolved

// Resolved is configuration that is resolved by looking up in the cluster
type Resolved struct {
	GcpProjectId string
	InstanceName string
	InstanceIp   string
}
