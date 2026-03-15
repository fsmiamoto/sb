FROM archlinux:latest

# Update system and install all packages in a single layer to avoid
# partial-upgrade issues and redundant pacman -Scc cache wipes.
RUN pacman -Syu --noconfirm && \
    pacman -S --noconfirm \
    # Core
    sudo \
    # Shell
    zsh \
    zsh-syntax-highlighting \
    zsh-autosuggestions \
    starship \
    strace \
    lazygit \
    # Editor
    neovim \
    # Build tools
    base-devel \
    cmake \
    # Languages
    python \
    python-pip \
    nodejs \
    npm \
    go \
    rust \
    # Utilities
    git \
    curl \
    wget \
    ripgrep \
    fd \
    jq \
    unzip \
    just \
    # Playwright browser dependencies
    libxcb \
    libx11 \
    libxext \
    libxrandr \
    libxcomposite \
    libxcursor \
    libxdamage \
    libxfixes \
    libxi \
    gtk3 \
    pango \
    atk \
    cairo \
    gdk-pixbuf2 \
    libxrender \
    alsa-lib \
    freetype2 \
    fontconfig \
    nss \
    nspr \
    dbus \
    at-spi2-core \
    cups \
    libdrm \
    mesa \
    && pacman -Scc --noconfirm

# Configure passwordless sudo for wheel group via drop-in file
# (avoids modifying /etc/sudoers directly, which bypasses visudo syntax checks)
RUN echo '%wheel ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/wheel-nopasswd && \
    chmod 0440 /etc/sudoers.d/wheel-nopasswd

# Install Claude Code to /home/sandbox/.local/bin
RUN HOME=/home/sandbox bash -c 'curl -fsSL https://claude.ai/install.sh | bash'

# Install global npm packages in a single layer and clean the cache
RUN npm install -g @openai/codex @mariozechner/pi-coding-agent agent-browser playwright && \
    npm cache clean --force

# Set up zsh as default shell
RUN chsh -s /bin/zsh

# Create user home directory structure with workspace
RUN mkdir -p /home/sandbox/.config /home/sandbox/.local/share /home/sandbox/.cache /home/sandbox/workspace

# Add user local bin and Go bin to PATH
ENV PATH="/home/sandbox/go/bin:/home/sandbox/.local/bin:${PATH}"

# Install Go CLI tools and clean the module cache
RUN GOPATH=/home/sandbox/go go install github.com/fsmiamoto/ralfinho/cmd/ralfinho@latest && \
    rm -rf /home/sandbox/go/pkg/mod/cache

# Install Playwright browsers to a shared location accessible by all users
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers
RUN mkdir -p /opt/playwright-browsers && \
    agent-browser install && \
    chmod -R 755 /opt/playwright-browsers

WORKDIR /home/sandbox/workspace

# Copy and set up entrypoint for dynamic user creation
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/bin/zsh"]
