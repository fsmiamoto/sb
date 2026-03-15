#!/bin/bash
set -euo pipefail

# Entrypoint script that creates a user entry for the specified UID/GID
# and then drops privileges to run as that user.

USER_ID="${HOST_UID:-1000}"
GROUP_ID="${HOST_GID:-1000}"

# Validate that UID and GID are non-negative integers
if ! [[ "$USER_ID" =~ ^[0-9]+$ ]]; then
    echo "error: HOST_UID must be a non-negative integer, got '$USER_ID'" >&2
    exit 1
fi
if ! [[ "$GROUP_ID" =~ ^[0-9]+$ ]]; then
    echo "error: HOST_GID must be a non-negative integer, got '$GROUP_ID'" >&2
    exit 1
fi

# Create group entry named "sandbox" with the specified GID
# Remove any existing group with this GID first to avoid conflicts
if getent group "$GROUP_ID" > /dev/null 2>&1; then
    # Get the existing group name and remove it if not "sandbox"
    existing_group=$(getent group "$GROUP_ID" | cut -d: -f1)
    if [ "$existing_group" != "sandbox" ]; then
        sed -i "/^${existing_group}:/d" /etc/group
        echo "sandbox:x:$GROUP_ID:" >> /etc/group
    fi
else
    echo "sandbox:x:$GROUP_ID:" >> /etc/group
fi

# Create passwd entry for sandbox user with the specified UID
if getent passwd "$USER_ID" > /dev/null 2>&1; then
    # Get the existing user name and remove it if not "sandbox"
    existing_user=$(getent passwd "$USER_ID" | cut -d: -f1)
    if [ "$existing_user" != "sandbox" ]; then
        sed -i "/^${existing_user}:/d" /etc/passwd
        sed -i "/^${existing_user}:/d" /etc/shadow
        echo "sandbox:x:$USER_ID:$GROUP_ID::/home/sandbox:/bin/zsh" >> /etc/passwd
    fi
else
    echo "sandbox:x:$USER_ID:$GROUP_ID::/home/sandbox:/bin/zsh" >> /etc/passwd
fi

# Ensure shadow entry exists for sandbox user (required for PAM/sudo)
if ! grep -q "^sandbox:" /etc/shadow; then
    echo "sandbox:!:19000:0:99999:7:::" >> /etc/shadow
fi

# Add sandbox user to wheel group for sudo access
if grep -q "^wheel:.*:$" /etc/group; then
    # wheel group has no members, add sandbox
    sed -i "s/^wheel:\(x:[0-9]*\):$/wheel:\1:sandbox/" /etc/group
elif ! grep -q "^wheel:.*sandbox" /etc/group; then
    # wheel group has members but not sandbox, append sandbox
    sed -i "s/^wheel:\(.*\)$/wheel:\1,sandbox/" /etc/group
fi

# Ensure home directory structure exists
mkdir -p /home/sandbox/.cache /home/sandbox/.local/share

# Fix ownership of container-created paths only. Avoid recursive chown on the
# entire home — bind-mounted directories (workspace, .claude, .gitconfig, etc.)
# already have correct host ownership, and recursing into a large workspace would
# be slow and unnecessarily update ctime on host files.
chown "$USER_ID:$GROUP_ID" /home/sandbox /home/sandbox/.config 2>/dev/null || true
chown -R "$USER_ID:$GROUP_ID" /home/sandbox/.cache /home/sandbox/.local
[ -d /home/sandbox/go ] && chown -R "$USER_ID:$GROUP_ID" /home/sandbox/go

# Drop privileges and execute the command
exec setpriv --reuid="$USER_ID" --regid="$GROUP_ID" --init-groups "$@"
