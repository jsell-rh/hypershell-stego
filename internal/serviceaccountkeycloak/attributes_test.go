package serviceaccountkeycloak

// Provider attribute names used by test fixtures. The generated managed-client
// helpers embed the same names; tests repeat them explicitly to construct
// provider representations.
const (
	managedAttribute          = "hypershell.service-account"
	gatewayAttribute          = "hypershell.gateway"
	gatewayIDAttribute        = "hypershell.gateway-id"
	serviceAccountIDAttribute = "hypershell.service-account-id"
	managedGatewayAttribute   = "stego.owner.hypershell.gateway"
	managedGatewayIDAttribute = "stego.owner.hypershell.gateway-id"
	managedConsoleAttribute   = "stego.owner.hypershell.console"
)
