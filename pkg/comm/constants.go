package comm

// default values
const (
	PodMsgGetTime             = 30
	QuotaInformerSyncDuration = 10
	ConnectTimeOut            = 5
	SleepTime                 = 20
	SleepTimeShort            = 2
	ZeroAlertRetryTime        = 20

	ServerlessTypeReplica = "replica"
	ServerlessTypeNoLimit = "noLimit"
	ServerlessTypeQPS     = "qps"
	True                  = "true"
	False                 = "false"

	ClusterAreaTypeCluster = "cluster"
	ClusterAreaTypeField   = "field"
	ClusterAreaTypeGlobal  = "global"

	SubscribeQPS = "-qps"

	PodSLSSelect = "apps.gaia.io/component"

	SlsPort = 6443
)
