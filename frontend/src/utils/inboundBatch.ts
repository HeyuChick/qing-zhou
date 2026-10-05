export interface BatchInbound { id:number; tag:string; enabled:boolean }
export interface BatchToggleFailure { id:number; name:string; error:string }

// Individual saves are not atomic. Keep every failure and only update a row
// after its own save succeeds; the caller retains failed selections for retry.
export async function applyInboundToggle<T extends BatchInbound>(targets:T[], enabled:boolean, save:(target:T)=>Promise<unknown>) {
  let updated=0, unchanged=0
  const failures:BatchToggleFailure[]=[]
  for(const target of targets){
    if(target.enabled===enabled){unchanged++;continue}
    try{await save(target);target.enabled=enabled;updated++}
    catch(error){failures.push({id:target.id,name:target.tag||`入站 #${target.id}`,error:error instanceof Error?error.message:'保存失败，请刷新核对'})}
  }
  return {updated,unchanged,failures}
}
