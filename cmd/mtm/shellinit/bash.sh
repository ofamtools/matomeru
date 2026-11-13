# mtm shell integration (bash 4.4+): OSC 133 marks A (prompt start), B (prompt
# end), C (command start, from PS0) and D;exit (command end). Output is stable
# for one mtm version; cache it: mtm shell-init bash > ~/.mtm-init.bash
if [[ $MTM_ENV == 1 && $- == *i* && -z ${__mtm_shell_init-} ]]; then
  __mtm_shell_init=1
  __mtm_hist=$HISTCMD
  # First in PROMPT_COMMAND so $? is still the command's status. A command ran
  # when the history number moved since the last prompt.
  __mtm_pc_first() {
    local code=$?
    if [[ $HISTCMD != "$__mtm_hist" ]]; then printf '\033]133;D;%s\007' "$code"; fi
    printf '\033]133;A\007'
    return "$code"
  }
  # Last in PROMPT_COMMAND so a prompt set by another hook still gets mark B.
  __mtm_pc_last() {
    __mtm_hist=$HISTCMD
    [[ $PS1 == *'133;B'* ]] || PS1+='\[\e]133;B\a\]'
  }
  if [[ ${PROMPT_COMMAND@a} == *a* ]]; then
    PROMPT_COMMAND=(__mtm_pc_first "${PROMPT_COMMAND[@]}" __mtm_pc_last)
  else
    PROMPT_COMMAND="__mtm_pc_first${PROMPT_COMMAND:+;$PROMPT_COMMAND};__mtm_pc_last"
  fi
  PS0+=$'\e]133;C\a'
fi
