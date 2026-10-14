#include "textflag.h"

TEXT ·sigactionTrampoline(SB),NOSPLIT,$0-0
	JMP process_sigaction(SB)
TEXT ·sigprocmaskTrampoline(SB),NOSPLIT,$0-0
	JMP process_sigprocmask(SB)
TEXT ·raiseTrampoline(SB),NOSPLIT,$0-0
	JMP process_raise(SB)
TEXT ·pidinfoTrampoline(SB),NOSPLIT,$0-0
	JMP process_pidinfo(SB)

TEXT ·sigactionAddress(SB),NOSPLIT,$0-8
	MOVD $·sigactionTrampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET
TEXT ·sigprocmaskAddress(SB),NOSPLIT,$0-8
	MOVD $·sigprocmaskTrampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET
TEXT ·raiseAddress(SB),NOSPLIT,$0-8
	MOVD $·raiseTrampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET
TEXT ·pidinfoAddress(SB),NOSPLIT,$0-8
	MOVD $·pidinfoTrampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET
