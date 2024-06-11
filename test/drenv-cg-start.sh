while ! drenv start envs/regional-dr.yaml ; 
do echo "----------------restart------------------" ; 
done ; 
echo succeed

while ! ./cephfscg/vgs.sh ;
do echo "----------------restart post scripts------------------" ; 
done ; 
echo succeed

while ! ../../youhangwang/vs-clean/build.sh ;
do echo "----------------delete vs/vgs clean opeartor------------------";
done ;
echo succeed

