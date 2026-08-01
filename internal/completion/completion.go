package completion

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

type DynamicValues struct {
	Profiles []string
	GateTags []string
	PackIDs  []string
}

func Generate(shell string, commands []cli.Command, globalFlags []cli.Flag, dynamic DynamicValues) (string, error) {
	commands = append([]cli.Command(nil), commands...)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		return bash(commands, globalFlags, dynamic), nil
	case "zsh":
		return zsh(commands, globalFlags, dynamic), nil
	case "fish":
		return fish(commands, globalFlags, dynamic), nil
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
}

func bash(commands []cli.Command, globalFlags []cli.Flag, dynamic DynamicValues) string {
	var out strings.Builder
	names := commandNames(commands)
	for _, command := range commands {
		fmt.Fprintf(&out, "# %s: %s\n", command.Name, strings.ReplaceAll(command.Summary, "\n", " "))
	}
	fmt.Fprintln(&out, "_vigil() {")
	fmt.Fprintln(&out, "  local cur prev cmd i")
	fmt.Fprintln(&out, "  COMPREPLY=()")
	fmt.Fprintln(&out, "  cur=\"${COMP_WORDS[COMP_CWORD]}\"")
	fmt.Fprintln(&out, "  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"")
	fmt.Fprintln(&out, "  cmd=\"\"")
	fmt.Fprintln(&out, "  for ((i=1; i<COMP_CWORD; i++)); do")
	fmt.Fprintln(&out, "    case \"${COMP_WORDS[i]}\" in")
	fmt.Fprintln(&out, "      --config) ((i++)) ;;")
	fmt.Fprintln(&out, "      --config=*) ;;")
	fmt.Fprintln(&out, "      --allow-mutation|--auto|--help|-h|--version) ;;")
	fmt.Fprintln(&out, "      -*) ;;")
	fmt.Fprintln(&out, "      *) cmd=\"${COMP_WORDS[i]}\"; break ;;")
	fmt.Fprintln(&out, "    esac")
	fmt.Fprintln(&out, "  done")
	fmt.Fprintln(&out, "  if [[ \"$prev\" == \"--config\" ]]; then")
	fmt.Fprintln(&out, "    COMPREPLY=( $(compgen -f -- \"$cur\") ); return")
	fmt.Fprintln(&out, "  fi")
	fmt.Fprintln(&out, "  if [[ -z \"$cmd\" ]]; then")
	fmt.Fprintf(&out, "    COMPREPLY=( $(compgen -W %s -- \"$cur\") ); return\n", bashQuote(strings.Join(append(names, flagNames(globalFlags)...), " ")))
	fmt.Fprintln(&out, "  fi")
	fmt.Fprintln(&out, "  case \"$cmd:$prev\" in")
	for _, command := range commands {
		for _, flag := range command.Flags {
			values := dynamicFlagValues(command.Name, flag, dynamic)
			switch {
			case len(values) > 0:
				fmt.Fprintf(&out, "    %s:%s) COMPREPLY=( $(compgen -W %s -- \"$cur\") ); return ;;\n", command.Name, flag.Long, bashQuote(strings.Join(values, " ")))
			case flag.File:
				fmt.Fprintf(&out, "    %s:%s) COMPREPLY=( $(compgen -f -- \"$cur\") ); return ;;\n", command.Name, flag.Long)
			}
		}
	}
	fmt.Fprintln(&out, "  esac")
	fmt.Fprintln(&out, "  case \"$cmd\" in")
	for _, command := range commands {
		var words []string
		words = append(words, flagNames(command.Flags)...)
		for _, argument := range command.Arguments {
			values := dynamicArgumentValues(command, argument, commands, dynamic)
			words = append(words, values...)
		}
		words = uniqueSorted(words)
		fmt.Fprintf(&out, "    %s) COMPREPLY=( $(compgen -W %s -- \"$cur\") )", command.Name, bashQuote(strings.Join(words, " ")))
		if commandHasFileArgument(command) {
			fmt.Fprint(&out, "; COMPREPLY+=( $(compgen -f -- \"$cur\") )")
		}
		fmt.Fprintln(&out, "; return ;;")
	}
	fmt.Fprintln(&out, "  esac")
	fmt.Fprintln(&out, "}")
	fmt.Fprintln(&out, "complete -F _vigil vigil")
	return out.String()
}

func zsh(commands []cli.Command, globalFlags []cli.Flag, dynamic DynamicValues) string {
	var out strings.Builder
	fmt.Fprintln(&out, "#compdef vigil")
	fmt.Fprintln(&out, "_vigil() {")
	fmt.Fprintln(&out, "  local context state line")
	fmt.Fprintln(&out, "  typeset -A opt_args")
	fmt.Fprintln(&out, "  local -a commands")
	fmt.Fprintln(&out, "  commands=(")
	for _, command := range commands {
		fmt.Fprintf(&out, "    %s\n", zshQuote(command.Name+":"+zshDescription(command.Summary)))
	}
	fmt.Fprintln(&out, "  )")
	fmt.Fprintln(&out, "  _arguments -C \\")
	for _, flag := range globalFlags {
		fmt.Fprintf(&out, "    %s \\\n", zshFlag(flag))
	}
	fmt.Fprintln(&out, "    '1:command:->command' \\")
	fmt.Fprintln(&out, "    '*::argument:->args'")
	fmt.Fprintln(&out, "  case $state in")
	fmt.Fprintln(&out, "    command) _describe 'command' commands ;;")
	fmt.Fprintln(&out, "    args)")
	fmt.Fprintln(&out, "      case $words[2] in")
	for _, command := range commands {
		fmt.Fprintf(&out, "        %s)\n", command.Name)
		fmt.Fprintln(&out, "          _arguments \\")
		specs := make([]string, 0, len(command.Flags)+len(command.Arguments))
		for _, flag := range command.Flags {
			specs = append(specs, zshFlagWithDynamic(command.Name, flag, dynamic))
		}
		for index, argument := range command.Arguments {
			specs = append(specs, zshArgument(index+1, command, argument, commands, dynamic))
		}
		if len(specs) == 0 {
			fmt.Fprintln(&out, "            '*:argument:'")
		} else {
			for index, spec := range specs {
				suffix := " \\"
				if index == len(specs)-1 {
					suffix = ""
				}
				fmt.Fprintf(&out, "            %s%s\n", spec, suffix)
			}
		}
		fmt.Fprintln(&out, "          ;;")
	}
	fmt.Fprintln(&out, "      esac")
	fmt.Fprintln(&out, "      ;;")
	fmt.Fprintln(&out, "  esac")
	fmt.Fprintln(&out, "}")
	fmt.Fprintln(&out, "_vigil \"$@\"")
	return out.String()
}

func fish(commands []cli.Command, globalFlags []cli.Flag, dynamic DynamicValues) string {
	var out strings.Builder
	fmt.Fprintln(&out, "complete -c vigil -f")
	for _, command := range commands {
		fmt.Fprintf(&out, "complete -c vigil -n '__fish_use_subcommand' -a %s -d %s\n", fishQuote(command.Name), fishQuote(command.Summary))
	}
	for _, flag := range globalFlags {
		fmt.Fprintf(&out, "complete -c vigil%s\n", fishFlag(flag, nil))
	}
	for _, command := range commands {
		condition := "__fish_seen_subcommand_from " + command.Name
		for _, flag := range command.Flags {
			values := dynamicFlagValues(command.Name, flag, dynamic)
			fmt.Fprintf(&out, "complete -c vigil -n %s%s\n", fishQuote(condition), fishFlag(flag, values))
		}
		for _, argument := range command.Arguments {
			values := dynamicArgumentValues(command, argument, commands, dynamic)
			if len(values) > 0 {
				fmt.Fprintf(&out, "complete -c vigil -n %s -a %s -d %s\n", fishQuote(condition), fishQuote(strings.Join(values, " ")), fishQuote(argument.Description))
			}
		}
	}
	return out.String()
}

func dynamicFlagValues(command string, flag cli.Flag, dynamic DynamicValues) []string {
	if len(flag.Values) > 0 {
		return append([]string(nil), flag.Values...)
	}
	switch flag.Long {
	case "--profile":
		return append([]string(nil), dynamic.Profiles...)
	case "--tag":
		return append([]string(nil), dynamic.GateTags...)
	default:
		return nil
	}
}

func dynamicArgumentValues(command cli.Command, argument cli.Argument, commands []cli.Command, dynamic DynamicValues) []string {
	if len(argument.Values) > 0 {
		return append([]string(nil), argument.Values...)
	}
	switch argument.Name {
	case "COMMAND":
		return commandNames(commands)
	case "PACK_ID", "EXTENSION_ID":
		return append([]string(nil), dynamic.PackIDs...)
	default:
		return nil
	}
}

func commandNames(commands []cli.Command) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}

func flagNames(flags []cli.Flag) []string {
	var names []string
	for _, flag := range flags {
		names = append(names, flag.Long)
		if flag.Short != "" {
			names = append(names, flag.Short)
		}
	}
	return names
}

func commandHasFileArgument(command cli.Command) bool {
	for _, argument := range command.Arguments {
		if argument.File {
			return true
		}
	}
	return false
}

func zshFlag(flag cli.Flag) string {
	names := flag.Long
	if flag.Short != "" {
		names = "{" + flag.Short + "," + flag.Long + "}"
	}
	value := ""
	if flag.ValueName != "" {
		if flag.File {
			value = ":" + flag.ValueName + ":_files"
		} else if len(flag.Values) > 0 {
			value = ":" + flag.ValueName + ":(" + strings.Join(flag.Values, " ") + ")"
		} else {
			value = ":" + flag.ValueName + ":"
		}
	}
	return zshQuote(names + "[" + zshDescription(flag.Description) + "]" + value)
}

func zshFlagWithDynamic(command string, flag cli.Flag, dynamic DynamicValues) string {
	copy := flag
	copy.Values = dynamicFlagValues(command, flag, dynamic)
	return zshFlag(copy)
}

func zshArgument(index int, command cli.Command, argument cli.Argument, commands []cli.Command, dynamic DynamicValues) string {
	prefix := strconv.Itoa(index) + ":"
	if !argument.Required {
		prefix = "::"
	}
	values := dynamicArgumentValues(command, argument, commands, dynamic)
	action := ""
	switch {
	case argument.File:
		action = "_files"
	case len(values) > 0:
		action = "(" + strings.Join(values, " ") + ")"
	}
	return zshQuote(prefix + argument.Description + ":" + action)
}

func fishFlag(flag cli.Flag, values []string) string {
	var out strings.Builder
	fmt.Fprintf(&out, " -l %s", strings.TrimPrefix(flag.Long, "--"))
	if flag.Short != "" {
		fmt.Fprintf(&out, " -s %s", strings.TrimPrefix(flag.Short, "-"))
	}
	fmt.Fprintf(&out, " -d %s", fishQuote(flag.Description))
	if flag.ValueName != "" || flag.File {
		fmt.Fprint(&out, " -r")
	}
	if len(values) > 0 {
		fmt.Fprintf(&out, " -a %s", fishQuote(strings.Join(values, " ")))
	}
	return out.String()
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func bashQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func zshQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func fishQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func zshDescription(value string) string {
	replacer := strings.NewReplacer("[", "(", "]", ")", ":", " -", "'", "")
	return replacer.Replace(value)
}
