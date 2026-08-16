package demo

// PairingWritable answers the GET /api/devices envelope in demo mode (qn.6p D7).
//
// ALWAYS WRITABLE, because the demo pairs nothing and persists nothing: its devices are fixtures
// and there is no lockdown directory behind them. Reporting `false` would render the Pair control
// unavailable with a reason that is not true of anything, which is worse than the control being
// offered and doing what the demo's other controls do.
func (p *Provider) PairingWritable() (bool, string) { return true, "" }
