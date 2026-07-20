{ pkgs ? import <nixpkgs> {} }:

pkgs.mkShell {
  packages = with pkgs; [
    coreutils
    curl
    go
    gnutar
    just
  ];
}
