"""Click-based CLI entry point for sb."""

from __future__ import annotations

import os
import re
import sys
from datetime import datetime

import click

from sb.config import load_config, merge_config
from sb.sandbox import SandboxInfo, SandboxManager


class AliasGroup(click.Group):
    """Click Group that supports command aliases."""

    ALIASES = {
        "a": "attach",
        "c": "create",
        "d": "destroy",
    }

    def get_command(self, ctx: click.Context, cmd_name: str) -> click.Command | None:
        """Resolve alias to real command name before lookup."""
        cmd_name = self.ALIASES.get(cmd_name, cmd_name)
        return super().get_command(ctx, cmd_name)

    def format_commands(
        self, ctx: click.Context, formatter: click.HelpFormatter
    ) -> None:
        """Show aliases alongside commands in help text."""
        commands = []
        for subcommand in self.list_commands(ctx):
            cmd = self.get_command(ctx, subcommand)
            if cmd is None or cmd.hidden:
                continue

            # Find alias for this command (reverse lookup)
            alias = next((a for a, c in self.ALIASES.items() if c == subcommand), None)
            if alias:
                name_display = f"{subcommand}, {alias}"
            else:
                name_display = subcommand

            help_text = cmd.get_short_help_str(limit=formatter.width)
            commands.append((name_display, help_text))

        if commands:
            with formatter.section("Commands"):
                formatter.write_dl(commands)


def _resolve_sandbox_by_name(manager: SandboxManager, query: str) -> SandboxInfo:
    """Resolve sandbox from partial name.

    Args:
        manager: The SandboxManager instance.
        query: The partial or full sandbox name to search for.

    Returns:
        The matching SandboxInfo.

    Raises:
        ValueError: If zero or multiple sandboxes match.
    """
    matches = manager.find_sandboxes(query)

    if not matches:
        raise ValueError(f"Sandbox '{query}' not found")

    if len(matches) == 1:
        return matches[0]

    # Multiple matches - format error message
    lines = [f"Multiple sandboxes match '{query}':"]
    for sandbox in matches:
        lines.append(f"  {sandbox.name}  ({sandbox.workspace})")
    lines.append("")
    lines.append("Use the full sandbox name or a more specific query.")

    raise ValueError("\n".join(lines))


@click.group(cls=AliasGroup)
@click.option(
    "--config",
    "-c",
    "config_path",
    type=click.Path(exists=True, dir_okay=False),
    default=None,
    help="Path to config file (default: ~/.config/sb/config.toml).",
)
@click.pass_context
def cli(ctx: click.Context, config_path: str | None) -> None:
    """sb: Docker sandbox tool for coding agents."""
    ctx.ensure_object(dict)

    # Load config from file and create SandboxManager
    file_config = load_config(config_path)
    merged = merge_config(file_config, {})

    # Store merged config for commands to use
    ctx.obj["config"] = merged

    # Create manager from merged config (deferred — only when a command needs it)
    # Commands that need Docker will call _get_manager(ctx) instead of SandboxManager()
    ctx.obj["manager"] = None
    ctx.obj["_manager_kwargs"] = {
        "extra_mounts": merged.get("extra_mounts", []),
        "env_passthrough": merged.get("env_passthrough", []),
        "custom_sensitive_dirs": merged.get("sensitive_dirs", []),
        **({"image_name": merged["image"]} if merged.get("image") else {}),
    }


def _get_manager(ctx: click.Context) -> SandboxManager:
    """Get or create the SandboxManager from context.

    Lazily initializes the manager on first access so that commands
    like ``--help`` don't require a running Docker daemon.
    """
    if ctx.obj["manager"] is None:
        try:
            ctx.obj["manager"] = SandboxManager(**ctx.obj["_manager_kwargs"])
        except RuntimeError as e:
            click.echo(f"Error: {e}", err=True)
            sys.exit(1)
    return ctx.obj["manager"]


@cli.command()
@click.option(
    "--name", "-n", help="Explicit sandbox name (auto-generated if not provided)"
)
@click.option(
    "--force", "-f", is_flag=True, help="Recreate sandbox if it already exists"
)
@click.option("--attach", "-a", is_flag=True, help="Attach to sandbox after creation")
@click.option("--mount", multiple=True, help="Additional read-only mount (repeatable)")
@click.option(
    "--env",
    multiple=True,
    help="Environment variable to pass (VAR or VAR=value, repeatable)",
)
@click.option("--image", help="Override Docker image to use")
@click.pass_context
def create(
    ctx: click.Context,
    name: str | None,
    force: bool,
    attach: bool,
    mount: tuple[str, ...],
    env: tuple[str, ...],
    image: str | None,
) -> None:
    """Create a new sandbox for the current directory."""
    manager = _get_manager(ctx)

    def confirm_callback(message: str) -> bool:
        """Confirmation callback using click.confirm()."""
        return click.confirm(message, default=False)

    def warn_callback(message: str) -> None:
        """Warning callback using click.echo to stderr."""
        click.echo(f"Warning: {message}", err=True)

    try:
        sandbox = manager.create(
            workspace=None,  # Use current directory
            name=name,
            force=force,
            extra_mounts=list(mount) if mount else None,
            env_vars=list(env) if env else None,
            image=image,
            confirm_callback=confirm_callback,
            warn_callback=warn_callback,
        )
        click.echo(
            f"Created sandbox '{sandbox.name}' for workspace '{sandbox.workspace}'"
        )

        if attach:
            container = manager.attach(name=sandbox.name)
            click.echo(f"Attached to sandbox '{sandbox.name}'")
            exit_code = manager.exec_shell(container)
            sys.exit(exit_code)
    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)
    except RuntimeError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)


@cli.command()
@click.argument("name", required=False)
@click.pass_context
def attach(ctx: click.Context, name: str | None) -> None:
    """Attach to a sandbox (auto-starts if stopped)."""
    manager = _get_manager(ctx)

    try:
        if name:
            # Use fuzzy matching to resolve the sandbox name
            sandbox = _resolve_sandbox_by_name(manager, name)
            container = manager.attach(name=sandbox.name)
        else:
            container = manager.attach(workspace=os.getcwd())
            sandbox = manager.get_sandbox_for_path(os.getcwd())

        sandbox_name = sandbox.name if sandbox else container.name

        click.echo(f"Attached to sandbox '{sandbox_name}'")

        # Execute interactive shell
        exit_code = manager.exec_shell(container)
        sys.exit(exit_code)

    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)
    except RuntimeError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)


@cli.command()
@click.argument("name", required=False)
@click.pass_context
def stop(ctx: click.Context, name: str | None) -> None:
    """Stop a running sandbox."""
    manager = _get_manager(ctx)

    try:
        if name:
            # Use fuzzy matching to resolve the sandbox name
            resolved = _resolve_sandbox_by_name(manager, name)
            sandbox = manager.stop(name=resolved.name)
        else:
            sandbox = manager.stop(workspace=os.getcwd())
        click.echo(f"Stopped sandbox '{sandbox.name}'.")
    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)
    except RuntimeError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)


@cli.command()
@click.argument("name", required=False)
@click.option("--force", "-f", is_flag=True, help="Skip confirmation prompt.")
@click.pass_context
def destroy(ctx: click.Context, name: str | None, force: bool) -> None:
    """Remove a sandbox completely."""

    def confirm_callback(msg: str) -> bool:
        return click.confirm(msg, default=False)

    manager = _get_manager(ctx)

    try:
        if name:
            # Use fuzzy matching to resolve the sandbox name
            resolved = _resolve_sandbox_by_name(manager, name)
            sandbox = manager.destroy(
                name=resolved.name,
                force=force,
                confirm_callback=confirm_callback if not force else None,
            )
        else:
            sandbox = manager.destroy(
                force=force,
                confirm_callback=confirm_callback if not force else None,
            )
        click.echo(f"Destroyed sandbox '{sandbox.name}'.")
    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)
    except RuntimeError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)


def _format_created_at(created_at: str) -> str:
    """Format an ISO timestamp for display.

    Handles Docker's timestamp format with nanosecond precision
    (e.g., 2024-01-25T10:30:00.123456789Z).
    """
    try:
        # Docker timestamps have nanosecond precision (9 digits after decimal)
        # Python's fromisoformat() only handles up to 6 digits (microseconds)
        # Truncate to 6 digits if needed
        timestamp = re.sub(r"\.(\d{6})\d+", r".\1", created_at)
        timestamp = timestamp.replace("Z", "+00:00")
        dt = datetime.fromisoformat(timestamp)
        return dt.strftime("%Y-%m-%d %H:%M")
    except (ValueError, AttributeError):
        return created_at[:16] if len(created_at) >= 16 else created_at


def _print_sandboxes_rich(
    sandboxes: list[SandboxInfo], manager: SandboxManager
) -> None:
    """Print sandbox table using rich library."""
    from rich.console import Console
    from rich.table import Table

    console = Console()
    table = Table(title="Sandboxes")

    table.add_column("Name", style="cyan", no_wrap=True)
    table.add_column("Workspace", style="white")
    table.add_column("Status", style="green")
    table.add_column("Created", style="dim")

    for sandbox in sandboxes:
        status = manager.get_container_status(sandbox)
        # Color status appropriately
        if status == "running":
            status_display = "[green]running[/green]"
        elif status == "exited" or status == "stopped":
            status_display = "[yellow]stopped[/yellow]"
        else:
            status_display = "[dim]unknown[/dim]"

        table.add_row(
            sandbox.name,
            sandbox.workspace,
            status_display,
            _format_created_at(sandbox.created_at),
        )

    console.print(table)


@cli.command(name="list")
@click.pass_context
def list_cmd(ctx: click.Context) -> None:
    """List all sandboxes with status."""
    manager = _get_manager(ctx)

    sandboxes = manager.list_sandboxes()

    if not sandboxes:
        click.echo("No sandboxes found. Use 'sb create' to create one.")
        return

    _print_sandboxes_rich(sandboxes, manager)


if __name__ == "__main__":
    cli()
