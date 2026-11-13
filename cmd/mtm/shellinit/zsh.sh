# mtm shell integration (zsh): OSC 133 marks A (prompt start), B (prompt end),
# C (command start) and D;exit (command end). Output is stable for one mtm
# version; cache it: mtm shell-init zsh > ~/.mtm-init.zsh
if [[ $MTM_ENV == 1 && -o interactive ]] && (( ! $+functions[__mtm_precmd] )); then
  __mtm_ran=
  __mtm_preexec() { __mtm_ran=1; printf '\033]133;C\007'; }
  # First in precmd_functions so $? is still the command's status.
  __mtm_precmd() {
    local code=$?
    if [[ -n $__mtm_ran ]]; then printf '\033]133;D;%s\007' "$code"; __mtm_ran=; fi
    printf '\033]133;A\007'
  }
  # Last in precmd_functions so a prompt set by another hook still gets mark B.
  __mtm_prompt_end() {
    if [[ $PROMPT != *$'\e]133;B'* ]]; then PROMPT+=$'%{\e]133;B\a%}'; fi
  }
  autoload -Uz add-zsh-hook
  add-zsh-hook preexec __mtm_preexec
  precmd_functions=(__mtm_precmd "${precmd_functions[@]}")
  add-zsh-hook precmd __mtm_prompt_end
  __mtm_prompt_end
fi
