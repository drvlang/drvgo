"""Build a consumer flake with independently selectable commands and shared Go sources."""
import json
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
flags = ['--no-write-lock-file', '--override-input', 'drvgo', repo,
         '--option', 'allow-import-from-derivation', 'false']

with tempfile.TemporaryDirectory(prefix='drvgo-monorepo-') as tmp:
    project = Path(tmp).resolve()

    def write(name, content):
        target = project / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def nix(*args):
        return subprocess.check_output(['nix', *args], cwd=project, text=True).strip()

    def planners():
        return json.loads(nix('eval', *flags, '--json', f'.#packages.{system}', '--apply',
                              'builtins.mapAttrs (_: command: command.planner.drvPath)'))

    def run_command(name):
        return nix('run', *flags, f'.#{name}', '--', 'two words')

    names = ['api', 'worker']
    system = nix('eval', '--impure', '--raw', '--expr', 'builtins.currentSystem')
    flake = '''{
      inputs.drvgo.url = "github:ryantm/drvgo";
      outputs = { drvgo, ... }: drvgo.lib.mkMonorepoFlake {
        src = builtins.path { path = ./.; name = "source"; filter = p: _: p != toString ./docs; };
        ldflags = "-X main.release=ci";
      };
    }'''
    write('flake.nix', flake)
    write('go.mod', 'module example.org/services\n\ngo 1.25\n')
    write('pkg/shared/shared.go', 'package shared\nimport _ "embed"\n//go:embed message.txt\nvar Message string\n')
    write('pkg/shared/message.txt', 'first')
    write('app/internal/internal.go', 'package internal\nconst Value = "internal"\n')
    write('app/web/index.ts', 'export default 1')
    write('pkg/frontend/index.ts', 'export default 2')
    write('cmd/legacy/main.go', 'package main\nfunc main() {}\n')
    command = '''package main
import ("fmt"; "os"; "example.org/services/pkg/shared"; "example.org/services/app/internal")
var release string
func main() { fmt.Println("COMMAND", shared.Message, internal.Value, release, os.Args[1]) }
'''
    for name in names:
        write(f'app/{name}/entry.go', command.replace('COMMAND', name))

    for output in ['packages', 'apps', 'checks']:
        listed = json.loads(nix('eval', *flags, '--json', f'.#{output}', '--apply',
                                'builtins.mapAttrs (_: builtins.attrNames)'))
        assert listed and all(value == names for value in listed.values()), (output, listed)
    nix('flake', 'check', *flags)
    for name in names:
        assert run_command(name) == f'{name} first internal ci two words'
    original = planners()
    write('docs/guide.md', 'irrelevant')
    assert planners() == original
    write('app/api/entry.go', command.replace('COMMAND', 'edited'))
    edited = planners()
    assert edited['api'] != original['api'] and edited['worker'] == original['worker']
    write('pkg/shared/message.txt', 'second')
    assert all(drv != edited[name] for name, drv in planners().items())
    assert run_command('api') == 'edited second internal ci two words'
    assert run_command('worker') == 'worker second internal ci two words'

    write('app/helper/helper.go', 'package helper\nconst Message = "shared"\n')
    write('flake.nix', flake.replace('ldflags =', 'commands = [ "api" "worker" ]; ldflags ='))
    write('app/api/entry.go', 'package main\nimport ("fmt"; "example.org/services/app/helper")\n'
          'func main() { fmt.Println(helper.Message) }\n')
    assert run_command('api') == 'shared'
