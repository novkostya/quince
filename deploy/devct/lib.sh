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
	DEVCT_ROOT_SSH=''

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
		root_ssh) DEVCT_ROOT_SSH=$_val ;;
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

# devct_perms — fetch the token's permission map once, cached in DEVCT_PERMS.
devct_perms() {
	[ -n "${DEVCT_PERMS:-}" ] && return 0
	_r=$(devct_api GET /access/permissions) || devct_die "cannot read /access/permissions (HTTP $(devct_code "$_r"))"
	DEVCT_PERMS=$(devct_body "$_r" | jq -c '.data')
}

# devct_has_priv <api-path> <privilege> — true if the token holds it. The permission map is the
# authority; a recorded grant is not.
devct_has_priv() {
	devct_perms
	printf '%s' "$DEVCT_PERMS" | jq -e --arg p "$1" --arg v "$2" '.[$p][$v] // empty' >/dev/null 2>&1
}

# ---------------------------------------------------------------------------
# Containers — shared by `devct` and `devct-template`, because the lifecycle verbs and the
# template build ask the same questions of the same boxes. One implementation, one place to fix
# a bug like "the conversion flag lands asynchronously".
# ---------------------------------------------------------------------------

# The guard: nothing here — a destroy, a root command, a start — is ever aimed at a vmid outside
# the token's pool. The token's own scope is the second line of defence, not the first.
devct_in_pool() {
	_r=$(devct_api GET "/pools/$DEVCT_POOL") || return 1
	devct_body "$_r" | jq -e --arg v "$1" '.data.members[]? | select((.vmid|tostring)==$v)' >/dev/null 2>&1
}

# devct_is_template <vmid> [seconds] — poll until the conversion flag is visible (it lands after
# the API call returns, which once produced a false "not a template" on a template).
devct_is_template() {
	_n=0
	while :; do
		_r=$(devct_api GET "/nodes/$DEVCT_NODE/lxc/$1/config") &&
			[ "$(devct_body "$_r" | jq -r '.data.template // 0')" = 1 ] && return 0
		[ "$_n" -ge "${2:-0}" ] && return 1
		sleep 5
		_n=$((_n + 5))
	done
}

devct_status() {
	_r=$(devct_api GET "/nodes/$DEVCT_NODE/lxc/$1/status/current") || return 1
	devct_body "$_r" | jq -r '.data.status // "unknown"'
}

devct_start() {
	[ "$(devct_status "$1")" = running ] && return 0
	_r=$(devct_api POST "/nodes/$DEVCT_NODE/lxc/$1/status/start") || return 1
	devct_task_wait "$(devct_body "$_r" | jq -r '.data')"
}

devct_destroy() {
	devct_in_pool "$1" || devct_die "vmid $1 is not in pool $DEVCT_POOL — refusing to destroy it"
	if [ "$(devct_status "$1")" = running ]; then
		_r=$(devct_api POST "/nodes/$DEVCT_NODE/lxc/$1/status/stop") &&
			devct_task_wait "$(devct_body "$_r" | jq -r '.data')" ||
			devct_die "could not stop $1"
	fi
	_r=$(devct_api DELETE "/nodes/$DEVCT_NODE/lxc/$1") ||
		devct_die "destroy refused (HTTP $(devct_code "$_r")): $(devct_body "$_r")"
	devct_task_wait "$(devct_body "$_r" | jq -r '.data')"
}

# Try first, THEN check the budget — a wait with a 0s budget still gets one attempt, which is
# what a caller asking "what is its address right now" means. Checking the counter first made
# `wait 0` skip the request entirely and report failure, which silently wrote an empty ssh
# include and reported success.
devct_wait_for_ip() {
	_n=0
	while :; do
		_r=$(devct_api GET "/nodes/$DEVCT_NODE/lxc/$1/interfaces") && {
			_addr=$(devct_body "$_r" | jq -r '[.data[]? | select(.name!="lo") | .inet // empty] | first // empty' | cut -d/ -f1)
			[ -n "$_addr" ] && {
				printf '%s\n' "$_addr"
				return 0
			}
		}
		[ "$_n" -ge "${2:-60}" ] && return 1
		sleep 5
		_n=$((_n + 5))
	done
}

# -n on every ssh: an ssh without it eats the stdin of whatever loop is calling it, which cost
# this tree three silently-skipped commands once already.
devct_wait_for_ssh() {
	_n=0
	while [ "$_n" -lt "${2:-120}" ]; do
		ssh -n -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 -o BatchMode=yes \
			"root@$1" true 2>/dev/null && return 0
		sleep 5
		_n=$((_n + 5))
	done
	return 1
}

# devct_task_wait <upid> — poll a PVE task to completion. Returns non-zero on a failed task,
# printing its exit status, because a task that starts is not a task that worked.
devct_task_wait() {
	_upid=$1
	_waited=0
	while :; do
		_r=$(devct_api GET "/nodes/$DEVCT_NODE/tasks/$_upid/status") ||
			devct_die "task status unreadable (HTTP $(devct_code "$_r"))"
		_status=$(devct_body "$_r" | jq -r '.data.status // "unknown"')
		[ "$_status" = running ] || break
		[ "$_waited" -ge "${DEVCT_TASK_TIMEOUT:-1800}" ] &&
			devct_die "task still running after ${_waited}s: $_upid"
		sleep 5
		_waited=$((_waited + 5))
	done
	_exit=$(devct_body "$_r" | jq -r '.data.exitstatus // "?"')
	[ "$_exit" = OK ] || {
		printf 'task failed: %s\n' "$_exit" >&2
		return 1
	}
	return 0
}
