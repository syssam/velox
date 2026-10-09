#!/usr/bin/env bash
# A `go build -toolexec` wrapper that records what each compile costs.
#
# For every compile and link it appends one line to the file named by $TXLOG:
#
#	<compile|link> <import path> <real s> <user s> <sys s> <max RSS bytes>
#
# Wall-clock time of a package depends on what else the machine is doing; user
# plus sys CPU time and max RSS depend much less on it. Compare those.
tool="$1"
shift
name=$(basename "$tool")
# Without $TXLOG there is nowhere to record: run the tool as it is. (Its
# output is cached with the package, so a complaint printed here would be
# replayed by every later build.)
if [[ -n "${TXLOG:-}" && ("$name" == "compile" || "$name" == "link") && -n "${TOOLEXEC_IMPORTPATH:-}" ]]; then
	t="$TXLOG.$$.$RANDOM"
	/usr/bin/time -l -o "$t" "$tool" "$@"
	rc=$?
	awk -v k="$name" -v p="$TOOLEXEC_IMPORTPATH" \
		'/ real /{r=$1;u=$3;s=$5} /maximum resident set size/{m=$1} END{print k, p, r, u, s, m}' "$t" >>"$TXLOG"
	rm -f "$t"
	exit $rc
fi
exec "$tool" "$@"
