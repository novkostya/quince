# shellcheck shell=sh
# devct — shared helpers. Sourced by `devct` and `devct-template`, never executed.
#
# Two rules shape everything here:
#   1. No secret ever reaches argv. The API token secret is read into a shell variable and
#      handed to curl through a config on STDIN (`curl -K -`), so it is invisible to `ps`.
#      Proxmox's own documentation makes the same point about auth headers on command lines.
#   2. Nothing site-shaped is committed. Every host, node, pool, storage, bridge and registry
#      value comes from ~/.config/quince/devct.conf, which is never in git.

DEVCT_CONF_DEFAULT="$HOME/.config/quince/devct.conf"
DEVCT_TOKEN_DEFAULT="$HOME/.config/quince/proxmox-devct.token"

devct_info() { printf '%s\n' "$*"; }
devct_warn() { printf 'warning: %s\n' "$*" >&2; }
devct_die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# devct_need <command>... — fail loudly, naming what to install. No degraded mode.
devct_need() {
	for _c in "$@"; do
		command -v "$_c" >/dev/null 2>&1 || devct_die "missing required command: $_c"
	done
}

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
# Format: plain key=value lines, # comments, no shell execution (the file is read, never
# sourced — a config file is data, not code).

devct_load_conf() {
	_conf="${DEVCT_CONF:-$DEVCT_CONF_DEFAULT}"
	[ -f "$_conf" ] || devct_die "no config at $_conf — run: devct onboard"

	# 0600 or tighter: the file names infrastructure, and it sits beside credentials.
	_perm=$(devct_file_mode "$_conf")
	case "$_perm" in
	600 | 400) ;;
	*) devct_warn "$_conf is mode $_perm — expected 600" ;;
	esac

	# Every key is initialised, then assigned by an explicit `case` arm. The obvious shortcut —
	# eval'ing "DEVCT_$(upper $key)=$val" — would make the key list the only thing standing
	# between a config file and the shell, and it hides every assignment from static analysis
	# (shellcheck SC2153, which is how this design got fixed rather than suppressed). An arm per
	# key is a few more lines and no eval at all.
	DEVCT_API_HOST=''
	DEVCT_API_ADDR=''
	DEVCT_API_PORT=''
	DEVCT_NODE=''
	DEVCT_POOL=''
	DEVCT_STORAGE=''
	DEVCT_TEMPLATE_STORAGE=''
	DEVCT_BRIDGE=''
	DEVCT_SDN_ZONE=''
	DEVCT_TEMPLATE_NAME=''
	DEVCT_TOKEN_ID=''
	DEVCT_TOKEN_FILE=''
	DEVCT_SSH_KEY=''
	DEVCT_REGISTRY=''
	DEVCT_CA_PIN=''

	while IFS= read -r _line; do
		case "$_line" in
		'' | \#*) continue ;;
		esac
		_key=${_line%%=*}
		_val=${_line#*=}
		_key=$(printf '%s' "$_key" | tr -d ' \t')
		_val=${_val# }
		case "$_key" in
		api_host) DEVCT_API_HOST=$_val ;;
		api_addr) DEVCT_API_ADDR=$_val ;;
		api_port) DEVCT_API_PORT=$_val ;;
		node) DEVCT_NODE=$_val ;;
		pool) DEVCT_POOL=$_val ;;
		storage) DEVCT_STORAGE=$_val ;;
		template_storage) DEVCT_TEMPLATE_STORAGE=$_val ;;
		bridge) DEVCT_BRIDGE=$_val ;;
		sdn_zone) DEVCT_SDN_ZONE=$_val ;;
		template_name) DEVCT_TEMPLATE_NAME=$_val ;;
		token_id) DEVCT_TOKEN_ID=$_val ;;
		token_file) DEVCT_TOKEN_FILE=$_val ;;
		ssh_key) DEVCT_SSH_KEY=$_val ;;
		registry) DEVCT_REGISTRY=$_val ;;
		ca_pin) DEVCT_CA_PIN=$_val ;;
		*) devct_warn "unknown key in $_conf: $_key (ignored)" ;;
		esac
	done <"$_conf"

	[ -n "$DEVCT_API_PORT" ] || DEVCT_API_PORT=8006
	[ -n "$DEVCT_CA_PIN" ] || DEVCT_CA_PIN="$HOME/.config/quince/devct-api.pem"
	[ -n "$DEVCT_TOKEN_FILE" ] || DEVCT_TOKEN_FILE="$DEVCT_TOKEN_DEFAULT"
	DEVCT_API_BASE="https://${DEVCT_API_HOST}:${DEVCT_API_PORT}"
}

devct_file_mode() {
	# BSD and GNU stat disagree; try both rather than assuming a platform.
	stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1" 2>/dev/null || printf 'unknown'
}

# devct_require_conf <key>=<value>... — callers pass the pairs, so this stays eval-free too.
devct_require_conf() {
	for _pair in "$@"; do
		if [ -z "${_pair#*=}" ]; then
			devct_die "config key '${_pair%%=*}' is not set in ${DEVCT_CONF:-$DEVCT_CONF_DEFAULT}"
		fi
	done
}

# ---------------------------------------------------------------------------
# API
# ---------------------------------------------------------------------------
# devct_api <METHOD> <PATH> [key=value ...]
#   Prints the HTTP status code on the FIRST line, the response body after it. Split with
#   devct_code / devct_body. Returns 0 for 2xx, 1 otherwise — the caller decides whether a
#   non-2xx is fatal, because for `doctor` a 403 is a *finding*, not a crash.
#
#   Why the code travels in the output rather than in a variable: callers capture this with
#   `$(...)`, which runs a subshell, so any variable set in here would be discarded on return.
devct_code() { printf '%s\n' "$1" | head -n 1; }
devct_body() { printf '%s\n' "$1" | tail -n +2; }

devct_api() {
	_method=$1
	_path=$2
	shift 2

	[ -n "${DEVCT_TOKEN_ID:-}" ] || devct_die "config key 'token_id' is not set (user@realm!tokenid)"
	[ -f "$DEVCT_TOKEN_FILE" ] || devct_die "token file not found: $DEVCT_TOKEN_FILE"
	[ -f "$DEVCT_CA_PIN" ] || devct_die "TLS pin not found: $DEVCT_CA_PIN — run: devct onboard (never -k)"

	_secret=$(cat "$DEVCT_TOKEN_FILE")
	_out=$(
		{
			printf 'url = "%s/api2/json%s"\n' "$DEVCT_API_BASE" "$_path"
			printf 'request = "%s"\n' "$_method"
			printf 'header = "Authorization: PVEAPIToken=%s=%s"\n' "$DEVCT_TOKEN_ID" "$_secret"
			printf 'cacert = "%s"\n' "$DEVCT_CA_PIN"
			printf 'silent\nshow-error\n'
			# api_host must be a name the API certificate actually carries — the URL host is
			# what curl verifies against, and PVE's self-signed cert names the node, not its
			# address. Where that name doesn't resolve, api_addr supplies the binding here
			# instead of in DNS, so verification stays on. (Found the hard way: an IP in
			# api_host fails with "no alternative certificate subject name matches".)
			if [ -n "${DEVCT_API_ADDR:-}" ]; then
				printf 'resolve = "%s:%s:%s"\n' \
					"$DEVCT_API_HOST" "$DEVCT_API_PORT" "$DEVCT_API_ADDR"
			fi
			if [ "$_method" = GET ]; then printf 'get\n'; fi
			for _kv in "$@"; do
				printf 'data-urlencode = "%s"\n' "$_kv"
			done
			printf 'write-out = "\\n%%{http_code}"\n'
		} | curl -K - 2>&1
	)
	_secret=

	_code=$(printf '%s' "$_out" | tail -n 1)
	printf '%s\n' "$_code"
	printf '%s\n' "$_out" | sed '$d'

	case "$_code" in
	2*) return 0 ;;
	*) return 1 ;;
	esac
}
