import type {AgentClient,ContextOperations,QueueOperations,JobOperations,OperatorOperations,KnowledgeOperations,NewSessionOperations,SkillOperations} from '@personal-agent/client';

// Read-only Ink navigation. Apply selection inside the same continuation as the
// generation check, so no asynchronous handoff separates checking and selection.
export async function resumeSession(client:AgentClient,target:string,context:ContextOperations,queue:QueueOperations,select:(sessionID:string)=>void,jobs?:JobOperations,operator?:OperatorOperations,knowledge?:KnowledgeOperations,admissions?:NewSessionOperations,skills?:SkillOperations):Promise<boolean>{
 const skillGeneration=skills?.beginNavigation();if(skillGeneration===null)return false;
 const admissionGeneration=admissions?.beginNavigation();if(admissionGeneration===null)return false;
 const knowledgeGeneration=knowledge?.beginNavigation();if(knowledgeGeneration===null)return false;
 const operatorGeneration=operator?.beginNavigation();if(operatorGeneration===null)return false;
 const jobGeneration=jobs?.beginNavigation();if(jobGeneration===null)return false;
 const queueGeneration=queue.beginNavigation();if(queueGeneration===null)return false;
 const generation=context.beginNavigation();if(generation===null)return false;
 await client.snapshot(target);
 if(skills&&skillGeneration!==undefined&&!skills.isNavigationCurrent(skillGeneration)||admissions&&admissionGeneration!==undefined&&!admissions.isNavigationCurrent(admissionGeneration)||knowledge&&knowledgeGeneration!==undefined&&!knowledge.isNavigationCurrent(knowledgeGeneration)||operator&&operatorGeneration!==undefined&&!operator.isNavigationCurrent(operatorGeneration)||!context.isNavigationCurrent(generation)||!queue.isNavigationCurrent(queueGeneration)||jobs&&jobGeneration!==undefined&&!jobs.isNavigationCurrent(jobGeneration))return false;
 select(target);return true;
}
