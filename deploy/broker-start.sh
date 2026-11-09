#!/bin/sh
set -eu
mkdir -p /run/viewhub
cp /viewhub-input/acl /run/viewhub/acl
chown mosquitto:mosquitto /run/viewhub/acl
chmod 640 /run/viewhub/acl

update_passwords() {
    umask 077
    cat /viewhub-input/passwords > /run/viewhub/passwords.next
    if [ -f /provision/passwords ]; then
        printf '\n' >> /run/viewhub/passwords.next
        cat /provision/passwords >> /run/viewhub/passwords.next
    fi
    if cmp -s /run/viewhub/passwords.next /run/viewhub/passwords; then
        rm /run/viewhub/passwords.next
        return 1
    fi
    chown mosquitto:mosquitto /run/viewhub/passwords.next
    mv /run/viewhub/passwords.next /run/viewhub/passwords
}

update_passwords || :
mosquitto -c /mosquitto/config/mosquitto.conf &
broker=$!
trap 'kill -TERM "$broker" 2>/dev/null || :; wait "$broker" || :' TERM INT
while kill -0 "$broker" 2>/dev/null; do
    sleep 2
    if update_passwords; then kill -HUP "$broker" 2>/dev/null || :; fi
done
wait "$broker"
