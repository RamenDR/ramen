# Setting up a local git server

## Initial setup

1. Install lighttpd

   ```
   sudo dnf install lighttpd
   ```

1. Create the git repo

   Create a directory where the git repositories will be served:

   ```
   sudo mkdir /var/www/gitlap
   cd /var/www/gitlap
   sudo git clone --bare https://github.com/nirs/ocm-kubevirt-samples.git
   ```

   Set git repo permissions so you can push changes, and the web server
   can serve the repo.

   ```
   sudo chown -R $USER:lighttpd /var/www/gitlap
   ```

1. Copy the vhost configuration

   ```
   sudo cp gitlap.conf /etc/lighttpd/vhosts.d/
   ```

1. Uncomment the vhost include in /etc/lighttpd/lighttpd.conf

   ```
   include conf_dir + "/vhosts.d/*.conf"
   ```

1. Enable and start the service

   ```
   sudo systemctl enable --now lighttpd
   ```

1. Allow http access in the libvirt zone

   ```
   sudo firewall-cmd --zone=libvirt --add-service=http --permanent
   sudo firewall-cmd --reload
   ```

## Testing the server

1. Add entry in /etc/hosts for testing locally

   ```
   192.168.122.1    host.minikube.internal
   ```

1. Check that git clone works

   ```
   git clone http://host.minikube.internal/ocm-kubevirt-samples.git
   rm -rf ocm-kubevirt-samples
   ```

1. Check git clone in a minikube cluster

   ```
   minikube ssh -p dr1
   git clone http://host.minikube.internal/ocm-kubevirt-samples.git
   rm -rf ocm-kubevirt-samples
   ```

## Updating the git repo

1. Add a remote to your working repo

   ```
   git remote add gitlap file:///var/www/gitlap/ocm-kubevirt-samples.git
   ```

1. Push changes to the remote

   ```
   git push -f gitlap main
   ```
