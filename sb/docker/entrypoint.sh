#!/bin/bash
# Entrypoint script that creates a user entry for the specified UID/GID
# and then drops privileges to run as that user.

USER_ID="${HOST_UID:-1000}"
GROUP_ID="${HOST_GID:-1000}"

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
        echo "sandbox:x:$USER_ID:$GROUP_ID::/home/sandbox:/bin/zsh" >> /etc/passwd
    fi
else
    echo "sandbox:x:$USER_ID:$GROUP_ID::/home/sandbox:/bin/zsh" >> /etc/passwd
fi

# Add sandbox user to wheel group for sudo access
if grep -q "^wheel:.*:$" /etc/group; then
    # wheel group has no members, add sandbox
    sed -i "s/^wheel:\(x:[0-9]*\):$/wheel:\1:sandbox/" /etc/group
elif ! grep -q "^wheel:.*sandbox" /etc/group; then
    # wheel group has members but not sandbox, append sandbox
    sed -i "s/^wheel:\(.*\)$/wheel:\1,sandbox/" /etc/group
fi

# Ensure home directory exists and is owned correctly
mkdir -p /home/sandbox/.cache /home/sandbox/.local/share
chown -R "$USER_ID:$GROUP_ID" /home/sandbox

# Drop privileges and execute the command
exec setpriv --reuid="$USER_ID" --regid="$GROUP_ID" --init-groups "$@"
