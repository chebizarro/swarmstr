---
summary: "Deploy swarmstr with Ansible and Nix"
read_when:
  - Automating swarmstr deployment with Ansible
  - Using Nix/NixOS for swarmstr
title: "Ansible & Nix Deployment"
---

# Ansible & Nix Deployment

## Ansible

Automate swarmstr deployment across multiple servers with Ansible.

### Inventory

```ini
# inventory.ini
[swarmstr]
agent1.example.com ansible_user=swarmstr
agent2.example.com ansible_user=swarmstr
pi.home ansible_user=swarmstr ansible_host=192.168.1.100
```

### Playbook

```yaml
# deploy-swarmstr.yml
---
- name: Deploy swarmstr
  hosts: swarmstr
  become: no
  vars:
    swarmstr_version: "v0.1.0"
    swarmstr_arch: "{{ 'arm64' if ansible_architecture == 'aarch64' else 'amd64' }}"
    swarmstr_home: "{{ ansible_env.HOME }}/.swarmstr"

  tasks:
    - name: Create .local/bin directory
      file:
        path: "{{ ansible_env.HOME }}/.local/bin"
        state: directory
        mode: '0755'

    - name: Download swarmstr binary
      get_url:
        url: "https://github.com/yourorg/swarmstr/releases/download/{{ swarmstr_version }}/swarmstrd-linux-{{ swarmstr_arch }}"
        dest: "{{ ansible_env.HOME }}/.local/bin/swarmstrd"
        mode: '0755'

    - name: Create swarmstr config directory
      file:
        path: "{{ swarmstr_home }}"
        state: directory
        mode: '0700'

    - name: Deploy config.json
      template:
        src: templates/config.json.j2
        dest: "{{ swarmstr_home }}/config.json"
        mode: '0600'

    - name: Deploy .env file
      template:
        src: templates/env.j2
        dest: "{{ swarmstr_home }}/.env"
        mode: '0600'

    - name: Install systemd service
      command: "{{ ansible_env.HOME }}/.local/bin/swarmstrd gateway install"
      args:
        creates: "{{ ansible_env.HOME }}/.config/systemd/user/swarmstrd.service"

    - name: Start swarmstrd service
      systemd:
        name: swarmstrd
        state: started
        enabled: yes
        scope: user
```

### Config Template

```json5
// templates/config.json.j2
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": {{ swarmstr_relays | to_json }},
      "dmPolicy": "allowlist",
      "allowFrom": {{ swarmstr_allowlist | to_json }}
    }
  },
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}"
    }
  }
}
```

### Secrets with Ansible Vault

```bash
# Encrypt secrets file
ansible-vault encrypt group_vars/swarmstr/vault.yml

# Run with vault password
ansible-playbook deploy-swarmstr.yml --ask-vault-pass
```

```yaml
# group_vars/swarmstr/vault.yml
vault_nostr_private_key: "nsec1..."
vault_anthropic_api_key: "sk-ant-..."
```

## Nix / NixOS

For NixOS deployments, a swarmstr module can be defined in your system configuration.

### Basic NixOS Module

```nix
# modules/swarmstr.nix
{ config, lib, pkgs, ... }:

let
  cfg = config.services.swarmstr;
  swarmstrd = pkgs.callPackage ./swarmstrd.nix {};
in
{
  options.services.swarmstr = {
    enable = lib.mkEnableOption "swarmstr AI agent daemon";
    user = lib.mkOption { type = lib.types.str; default = "swarmstr"; };
    configFile = lib.mkOption { type = lib.types.path; };
    envFile = lib.mkOption { type = lib.types.path; };
  };

  config = lib.mkIf cfg.enable {
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.user;
      home = "/var/lib/swarmstr";
      createHome = true;
    };

    systemd.services.swarmstrd = {
      description = "swarmstr AI agent daemon";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = {
        User = cfg.user;
        ExecStart = "${swarmstrd}/bin/swarmstrd";
        EnvironmentFile = cfg.envFile;
        Restart = "always";
        RestartSec = "10s";
      };

      environment = {
        SWARMSTR_CONFIG = cfg.configFile;
      };
    };
  };
}
```

### Using the Module

```nix
# configuration.nix
{
  imports = [ ./modules/swarmstr.nix ];

  services.swarmstr = {
    enable = true;
    configFile = /etc/swarmstr/config.json;
    envFile = /etc/swarmstr/.env;
  };
}
```

## See Also

- [VPS Deploy Guides](/install/vps-guides)
- [Linux Platform Guide](/platforms/linux)
- [Updating & Uninstalling](/install/updating)
