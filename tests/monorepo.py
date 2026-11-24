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

    def expect_failed_test(test, message):
        # Let sibling builds finish before editing the fixture and checking it again.
        check = subprocess.run(['nix', 'flake', 'check', '-L', '--keep-going', *flags], cwd=project, text=True,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        assert check.returncode != 0 and test in check.stdout and message in check.stdout, check.stdout

    names = ['api', 'worker']
    system = nix('eval', '--impure', '--raw', '--expr', 'builtins.currentSystem')
    flake = '''{
      inputs.drvgo.url = "github:drvlang/drvgo";
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
        expected = sorted(names + ['module-tests/app/internal', 'module-tests/cmd/legacy',
                                   'module-tests/pkg/shared']) if output == 'checks' else names
        assert listed and all(value == expected for value in listed.values()), (output, listed)
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

    # A test-only library outside app/ must fail an ordinary flake check.
    project = project / 'botany'
    project.mkdir()
    write('go.mod', 'module example.org/botany\n\ngo 1.25\nignore ignored\n')
    write('app/fern/main.go', 'package main\nfunc main() {}\n')
    write('botany_test.go', 'package botany_test\nimport "testing"\nfunc TestRoot(t *testing.T) {}\n')
    write('garden/soil_test.go', 'package garden\nimport "testing"\nfunc TestSoil(t *testing.T) { t.Fatal("bad soil") }\n')
    write('garden/testdata/soil', 'loam')
    write('garden/internal/seed/seed.go', 'package seed\nconst Kind = "fern"\n')
    for subdir in ['garden/ignored', 'garden/.hidden', 'garden/_private', 'garden/testdata',
                   'garden/vendor/dep', 'garden/nested']:
        write(f'{subdir}/invalid_test.go', 'this is not valid Go\n')
    write('garden/nested/go.mod', 'module example.org/nested\n\ngo 1.25\n')
    botany = '''{
      inputs.drvgo.url = "github:drvlang/drvgo";
      outputs = { drvgo, ... }: drvgo.lib.mkMonorepoFlake {
        src = (drvgo.lib.projectSource { root = ./.; files = [ ./go.mod ./botany_test.go ./app ./garden ]; })
          // { outPath = throw "discovery imported the entire workspace"; };
      };
    }'''

    def outputs(attr):
        return json.loads(nix('eval', *flags, '--json', f'.#{attr}.{system}', '--apply', 'builtins.attrNames'))

    write('flake.nix', botany)
    assert outputs('apps') == outputs('packages') == ['fern']
    expected = ['fern', 'module-tests', 'module-tests/garden',
                'module-tests/garden/ignored', 'module-tests/garden/internal/seed']
    assert outputs('checks') == expected
    expect_failed_test('TestSoil', 'bad soil')
    write('garden/soil_test.go', '''package garden
import ("os"; "testing"; "example.org/botany/garden/internal/seed")
func TestSoil(t *testing.T) {
    soil, err := os.ReadFile("testdata/soil")
    if err != nil || string(soil) != "loam" || seed.Kind != "fern" { t.Fatal("bad soil") }
}
''')
    nix('flake', 'check', *flags)

    for old_setting in ['true', 'false']:
        write('flake.nix', botany.replace('src =', f'moduleTests = {old_setting}; src ='))
        assert outputs('checks') == expected

    scoped = botany.replace('src =', '''srcForTarget = { name, target }: drvgo.lib.projectSource {
          root = ./.;
          files = [ ./go.mod ] ++ (if target == "." then [ ./botany_test.go ] else [ (./. + "/${target}") ]);
        };
        src =''')
    write('flake.nix', scoped)
    garden_check = f'.#checks.{system}.module-tests/garden'
    recipe = nix('eval', *flags, '--raw', garden_check + '.drvPath')
    write('app/fern/main.go', 'package main\nfunc main() { println("fern") }\n')
    assert nix('eval', *flags, '--raw', garden_check + '.drvPath') == recipe
    nix('build', *flags, '--no-link', garden_check)

    # Without a module-wide src, discover across all supplied command sources.
    write('app/ivy/main.go', 'package main\nfunc main() {}\n')
    write('vines/vine_test.go', 'package vines\nimport "testing"\nfunc TestVine(t *testing.T) { t.Fatal("bad vine") }\n')
    write('outside/invalid_test.go', 'excluded from every source\n')
    write('flake.nix', '''{
      inputs.drvgo.url = "github:drvlang/drvgo";
      outputs = { drvgo, ... }: drvgo.lib.mkMonorepoFlake {
        commands = { fern = "app/fern"; ivy = "app/ivy"; };
        srcForTarget = { target, ... }: drvgo.lib.projectSource {
          root = ./.;
          files = [ ./go.mod ./garden (./. + "/${target}") ]
            ++ (if target == "app/ivy" then [ ./vines ] else []);
        };
      };
    }''')
    assert outputs('checks') == ['fern', 'ivy', 'module-tests/garden', 'module-tests/garden/ignored',
                                 'module-tests/garden/internal/seed', 'module-tests/vines']
    expect_failed_test('TestVine', 'bad vine')
    write('vines/vine_test.go', 'package vines\nimport "testing"\nfunc TestVine(t *testing.T) {}\n')
    nix('flake', 'check', *flags)
