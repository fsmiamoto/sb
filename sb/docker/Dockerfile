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

# Install Claude Code and Codex globally via npm
RUN npm install -g @anthropic-ai/claude-code @openai/codex

# Set up zsh as default shell
RUN chsh -s /bin/zsh

# Create user home directory structure with workspace
RUN mkdir -p /home/sandbox/.config /home/sandbox/.local/share /home/sandbox/.cache /home/sandbox/workspace

WORKDIR /home/sandbox/workspace

# Copy and set up entrypoint for dynamic user creation
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/bin/zsh"]
