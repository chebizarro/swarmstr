---
summary: "Deploy metiq with Ansible and Nix"
read_when:
  - Automating metiq deployment with Ansible
  - Using Nix/NixOS for metiq
title: "Ansible & Nix Deployment"
---

# Ansible & Nix Deployment

## Ansible

Automate metiq deployment across multiple servers with Ansible.

### Inventory

```ini
# inventory.ini
[metiq]
agent1.example.com ansible_user=metiq
agent2.example.com ansible_user=metiq
pi.home ansible_user=metiq ansible_host=192.168.1.100
```

### Playbook

```yaml
# deploy-metiq.yml
---
- name: Deploy metiq
  hosts: metiq
  become: no
  vars:
    metiq_version: "v0.1.0"
    metiq_arch: "{{ 'arm64' if ansible_architecture == 'aarch64' else 'amd64' }}"
    metiq_home: "{{ ansible_env.HOME }}/.metiq"

  tasks:
    - name: Create .local/bin directory
      file:
        path: "{{ ansible_env.HOME }}/.local/bin"
        state: directory
        mode: '0755'

    - name: Download metiq binary
      get_url:
        url: "https://github.com/yourorg/metiq/releases/download/{{ metiq_version }}/metiqd-linux-{{ metiq_arch }}"
        dest: "{{ ansible_env.HOME }}/.local/bin/metiqd"
        mode: '0755'

    - name: Create metiq config directory
      file:
        path: "{{ metiq_home }}"
        state: directory
        mode: '0700'

    - name: Deploy bootstrap.json
      template:
        src: templates/bootstrap.json.j2
        dest: "{{ metiq_home }}/bootstrap.json"
        mode: '0600'

    - name: Deploy config.json
      template:
        src: templates/config.json.j2
        dest: "{{ metiq_home }}/config.json"
        mode: '0600'

    - name: Deploy env file
      template:
        src: templates/env.j2
        dest: "{{ metiq_home }}/env"
        mode: '0600'

    - name: Create systemd user directory
      file:
        path: "{{ ansible_env.HOME }}/.config/systemd/user"
        state: directory
        mode: '0755'

    - name: Install systemd service
      template:
        src: templates/metiqd.service.j2
        dest: "{{ ansible_env.HOME }}/.config/systemd/user/metiqd.service"
        mode: '0644'

    - name: Reload systemd user daemon
      systemd:
        daemon_reload: yes
        scope: user

    - name: Start metiqd service
      systemd:
        name: metiqd
        state: started
        enabled: yes
        scope: user
```

### Config Templates

```json
// templates/bootstrap.json.j2
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": {{ metiq_relays | to_json }},
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${METIQ_ADMIN_TOKEN}"
}
```

```json
// templates/config.json.j2
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": {
    "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" }
  },
  "dm": { "policy": "allowlist", "allow_from": {{ metiq_allowlist | to_json }} }
}
```

```ini
# templates/metiqd.service.j2
[Unit]
Description=metiq AI agent daemon
After=network.target

[Service]
Type=simple
ExecStart={{ ansible_env.HOME }}/.local/bin/metiqd
Restart=on-failure
RestartSec=5
EnvironmentFile={{ ansible_env.HOME }}/.metiq/env

[Install]
WantedBy=default.target
```

### Secrets with Ansible Vault

```bash
# Encrypt secrets file
ansible-vault encrypt group_vars/metiq/vault.yml

# Run with vault password
ansible-playbook deploy-metiq.yml --ask-vault-pass
```

```yaml
# group_vars/metiq/vault.yml
vault_nostr_private_key: "nsec1..."
vault_anthropic_api_key: "sk-ant-..."
```

## Nix / NixOS

For NixOS deployments, a metiq module can be defined in your system configuration.

### Basic NixOS Module

```nix
# modules/metiq.nix
{ config, lib, pkgs, ... }:

let
  cfg = config.services.metiq;
  metiqd = pkgs.callPackage ./metiqd.nix {};
in
{
  options.services.metiq = {
    enable = lib.mkEnableOption "metiq AI agent daemon";
    user = lib.mkOption { type = lib.types.str; default = "metiq"; };
    configFile = lib.mkOption { type = lib.types.path; };
    envFile = lib.mkOption { type = lib.types.path; };
  };

  config = lib.mkIf cfg.enable {
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.user;
      home = "/var/lib/metiq";
      createHome = true;
    };

    systemd.services.metiqd = {
      description = "metiq AI agent daemon";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = {
        User = cfg.user;
        ExecStart = "${metiqd}/bin/metiqd";
        EnvironmentFile = cfg.envFile;
        Restart = "always";
        RestartSec = "10s";
      };

      environment = {};
    };
  };
}
```

### Using the Module

```nix
# configuration.nix
{
  imports = [ ./modules/metiq.nix ];

  services.metiq = {
    enable = true;
    configFile = /etc/metiq/config.json;
    envFile = /etc/metiq/env;
  };
}
```

## See Also

- [VPS Deploy Guides](/install/vps-guides)
- [Linux Platform Guide](/platforms/linux)
- [Updating & Uninstalling](/install/updating)
