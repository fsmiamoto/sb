FROM archlinux:latest

# Update system and install core packages
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
    && pacman -Scc --noconfirm

# Configure passwordless sudo for wheel group
RUN echo '%wheel ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers

# Install Playwright browser dependencies (Arch equivalents)
# Use -Sy to refresh package database (mirrors may have updated since first RUN)
RUN pacman -Sy --noconfirm \
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

# Install Claude Code and Codex globally via npm
RUN npm install -g @anthropic-ai/claude-code @openai/codex
RUN npm install -g agent-browser playwright

# Set up zsh as default shell
RUN chsh -s /bin/zsh

# Create user home directory structure with workspace
RUN mkdir -p /home/sandbox/.config /home/sandbox/.local/share /home/sandbox/.cache /home/sandbox/workspace

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
